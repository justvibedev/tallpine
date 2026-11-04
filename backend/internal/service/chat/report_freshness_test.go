package chat_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

type reportChatDelivery struct{ svc *chatsvc.Service }

func (d reportChatDelivery) Submit(ctx context.Context, id domain.SessionID, text, key string) error {
	return d.svc.SubmitReports(ctx, id, text, key)
}
func (d reportChatDelivery) Interrupt(ctx context.Context, id domain.SessionID) error {
	return d.svc.Interrupt(ctx, id)
}

func seedReportWorker(t *testing.T, h *harness) domain.SessionRecord {
	t.Helper()
	rec, err := h.st.CreateSession(context.Background(), domain.SessionRecord{
		ID: "p1-worker", ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: h.now(), UpdatedAt: h.now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestOrdinaryReportsWakeIdleChatImmediately(t *testing.T) {
	for _, input := range []reportsvc.CreateInput{
		{Message: "informational update"},
		{State: domain.ReportCheckpoint, Note: "checkpoint", Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "checkpoint.html"}}},
		{State: domain.ReportDone, Note: "finished"},
		{Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result.html"}}},
	} {
		t.Run(string(input.State)+input.Message, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			worker := seedReportWorker(t, h)
			coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
			h.svc.SetReportCoordinator(coordinator)
			if delay, err := coordinator.RunDue(ctx); err != nil || delay != -1 {
				t.Fatalf("idle schedule = %v, %v", delay, err)
			}
			runCtx, cancel := context.WithCancel(ctx)
			stopped := coordinator.Start(runCtx)
			t.Cleanup(func() { cancel(); <-stopped })
			reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now, OnCreated: func(domain.ReportRecord) { coordinator.Wake() }})
			input.SessionID = worker.ID
			r, err := reports.Create(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			// OnCreated wakes the real scheduling loop without advancing fake time.
			h.awaitSnapshot(t, func(store.ConversationSnapshot) bool {
				got, _, err := h.st.GetReport(ctx, r.ID)
				return err == nil && got.DeliveryState == domain.ReportAcknowledged
			})
			if len(h.conv.sentTexts()) != 1 {
				t.Fatalf("idle provider received %d messages", len(h.conv.sentTexts()))
			}
			got, _, err := h.st.GetReport(ctx, r.ID)
			if err != nil || got.DeliveryState != domain.ReportAcknowledged {
				t.Fatalf("report = %+v, %v", got, err)
			}
			if _, err := coordinator.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			if len(h.conv.sentTexts()) != 1 {
				t.Fatal("report dispatched twice")
			}
		})
	}
}

// A plain checkpoint (no outputs) is filed Quiet since B192: it must not wake
// an idle orchestrator. The wake tests above use checkpoints with an output.
func TestPlainCheckpointDoesNotWakeIdleChat(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	h.svc.SetReportCoordinator(coordinator)
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	r, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, State: domain.ReportCheckpoint, Note: "progress"})
	if err != nil {
		t.Fatal(err)
	}
	if delay, err := coordinator.RunDue(ctx); err != nil || delay != reportsvc.DefaultQuietDelay {
		t.Fatalf("quiet delay=%v err=%v", delay, err)
	}
	got, _, err := h.st.GetReport(ctx, r.ID)
	if err != nil || got.DeliveryState == domain.ReportAcknowledged || len(h.conv.sentTexts()) != 0 {
		t.Fatalf("plain checkpoint woke chat: %+v, %v, sent=%d", got, err, len(h.conv.sentTexts()))
	}
}

func TestBusyReportsDrainOnProviderCompletion(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprint(restart), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			worker := seedReportWorker(t, h)
			coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
			h.svc.SetReportCoordinator(coordinator)
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatal(err)
			}
			reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
			// Filed straight into the outbox as an immediately-due checkpoint (what an
			// older daemon queued): plain checkpoints from Create are Quiet since B192.
			old, err := h.st.CreateReport(ctx, domain.ReportRecord{ID: "rpt-old", SessionID: worker.ID, ProjectID: testProject,
				State: domain.ReportCheckpoint, Note: "old progress", CreatedAt: h.now(), AvailableAt: h.now(), DeliveryState: domain.ReportPending, RepeatCount: 1})
			if err != nil {
				t.Fatal(err)
			}
			if delay, err := coordinator.RunDue(ctx); err != nil || delay != reportsvc.OrchestratorBusyRetry {
				t.Fatalf("busy delay=%v err=%v", delay, err)
			}
			h.advance(time.Second)
			latest, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, State: domain.ReportDone, Note: "latest result"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			if restart {
				coordinator = reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
				h.svc.SetReportCoordinator(coordinator)
			}
			runCtx, cancel := context.WithCancel(ctx)
			stopped := coordinator.Start(runCtx)
			t.Cleanup(func() { cancel(); <-stopped })
			completion := ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"}, completion, completion)
			h.awaitSnapshot(t, func(store.ConversationSnapshot) bool {
				for _, id := range []string{old.ID, latest.ID} {
					got, _, err := h.st.GetReport(ctx, id)
					if err != nil || got.DeliveryState != domain.ReportAcknowledged {
						return false
					}
				}
				return true
			})
			if got := h.conv.sentTexts(); len(got) != 2 || !strings.Contains(got[1], "latest result") || strings.Contains(got[1], "old progress") {
				t.Fatalf("sent=%v", got)
			}
			if err := h.svc.DrainQueued(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			if n, err := h.st.RequeueClaimedReports(ctx); err != nil || n != 0 {
				t.Fatalf("recovered=%d err=%v", n, err)
			}
			if len(h.conv.sentTexts()) != 2 {
				t.Fatal("duplicate completion redelivered report")
			}
		})
	}
}

func TestBusyReportsStayInOutboxAndPiggybackFreshAfterSteering(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	h.svc.SetReportCoordinator(coordinator)
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now, OnCreated: func(domain.ReportRecord) { coordinator.Wake() }})
	create := func(id string, state domain.ReportState, note string) {
		t.Helper()
		_, err := reports.Create(ctx, reportsvc.CreateInput{ID: id, SessionID: worker.ID, State: state, Note: note})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("old", domain.ReportCheckpoint, "old blocked build")
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	old, _, err := h.st.GetReport(ctx, "old")
	if err != nil || old.DeliveryState != domain.ReportPending {
		t.Fatalf("busy checkpoint = %+v, %v", old, err)
	}
	if _, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID()); !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatalf("report entered chat queue: %v", err)
	}
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "show the result", Origin: domain.MessageOriginHuman, ClientMessageID: "user-next"})
	if err != nil || queued.State != domain.TurnStateQueued {
		t.Fatalf("user send=%+v err=%v", queued, err)
	}
	stored, err := h.st.QueuedTurnMessage(ctx, h.ctrl.ConversationID(), queued.ID)
	if err != nil || stored.Text != "show the result" {
		t.Fatalf("premature report snapshot: %+v, %v", stored, err)
	}
	h.advance(time.Minute)
	create("done", domain.ReportDone, "release succeeded")
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentTexts()) != 1 {
		t.Fatal("immediate eligibility started concurrent work")
	}
	done, _, err := h.st.GetReport(ctx, "done")
	if err != nil || done.DeliveryState != domain.ReportPending {
		t.Fatalf("busy done report = %+v, %v", done, err)
	}
	if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "Use the latest result", Origin: domain.MessageOriginHuman, ClientMessageID: "steer-latest",
	}, false); err != nil {
		t.Fatal(err)
	}
	// The steer flushes the pending prompt into the running turn (batching with
	// steering); reports cannot ride a steer, so they stay in the outbox for the
	// next prompt that starts a turn.
	if steers := provider.steers(); len(steers) != 1 || !strings.Contains(steers[0].msg.Text, "show the result") ||
		strings.Contains(steers[0].msg.Text, "release succeeded") {
		t.Fatalf("steer = %+v", steers)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "now summarize", Origin: domain.MessageOriginHuman, ClientMessageID: "user-after-steer"}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(provider.sentTexts()) == 2 })
	got := provider.sentTexts()[1]
	if !strings.Contains(got, "now summarize") || !strings.Contains(got, "release succeeded") || strings.Contains(got, "old blocked build") {
		t.Fatalf("provider received stale context: %s", got)
	}
	for _, id := range []string{"old", "done"} {
		r, _, err := h.st.GetReport(ctx, id)
		if err != nil || r.DeliveryState != domain.ReportAcknowledged {
			t.Fatalf("report %s=%+v err=%v", id, r, err)
		}
	}
	if n, err := h.st.RequeueClaimedReports(ctx); err != nil || n != 0 {
		t.Fatalf("restart recovered an already consumed batch: %d %v", n, err)
	}
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(provider.sentTexts()) != 2 {
		t.Fatal("consumed reports caused a second turn")
	}
}

func TestReportAcceptanceRetryDoesNotDispatchTwice(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:retry"); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:retry"); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentTexts()) != 1 {
		t.Fatal("retry redelivered accepted batch")
	}
	if err := h.svc.SubmitReports(ctx, testSession, "next report", "report-batch:next"); !errors.Is(err, reportsvc.ErrOrchestratorBusy) {
		t.Fatalf("busy acceptance=%v", err)
	}
}

func TestLegacyQueuedReportIsRefreshedBeforeConsumption(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Now: h.now})
	h.svc.SetReportCoordinator(coordinator)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "long task", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	old, err := h.st.CreateReport(ctx, domain.ReportRecord{ID: "legacy", SessionID: worker.ID, ProjectID: testProject,
		State: domain.ReportStuck, Note: "stale blocked", CreatedAt: h.now(), AvailableAt: h.now(), DeliveryState: domain.ReportPending, RepeatCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	batch, err := coordinator.PreparePiggyback(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession, batch.Render(), batch.IdempotencyKey(), ports.MessageDeliveryOptions{})
	if err != nil || legacy.Outcome != ports.MessageQueued {
		t.Fatalf("legacy acceptance=%+v err=%v", legacy, err)
	}
	if err := coordinator.AcceptPiggyback(ctx, batch); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	_, err = h.st.CreateReport(ctx, domain.ReportRecord{ID: "progress", SessionID: worker.ID, ProjectID: testProject,
		State: domain.ReportCheckpoint, Note: "build progressing", CreatedAt: h.now(), AvailableAt: h.now().Add(time.Hour), DeliveryState: domain.ReportPending, RepeatCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "user follow-up", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 2 })
	got := h.conv.sentTexts()[1]
	if strings.Contains(got, old.Note) || !strings.Contains(got, "user follow-up") || !strings.Contains(got, "build progressing") {
		t.Fatalf("stale legacy report overtook user: %s", got)
	}
}

func TestReportPromptAttachmentRollsBackWithLostClaim(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "user text", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.st.CreateReport(ctx, domain.ReportRecord{ID: "atomic", SessionID: worker.ID, ProjectID: testProject,
		State: domain.ReportCheckpoint, Note: "progress", CreatedAt: h.now(), AvailableAt: h.now(), DeliveryState: domain.ReportPending, RepeatCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.st.ClaimPendingReportBatch(ctx, testProject, "", "correct-claim", h.now()); err != nil {
		t.Fatal(err)
	}
	message, err := h.st.QueuedTurnMessage(ctx, h.ctrl.ConversationID(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	message.Text = "user text with report"
	if err := h.st.CommitQueuedTurnReports(ctx, h.ctrl.ConversationID(), queued.ID, message, testProject, "wrong-claim", 1, h.now()); err == nil {
		t.Fatal("lost claim committed a prompt")
	}
	unchanged, err := h.st.QueuedTurnMessage(ctx, h.ctrl.ConversationID(), queued.ID)
	if err != nil || unchanged.Text != "user text" || unchanged.Revision != message.Revision {
		t.Fatalf("failed transaction changed prompt: %+v %v", unchanged, err)
	}
	if err := h.st.CommitQueuedTurnReports(ctx, h.ctrl.ConversationID(), queued.ID, message, testProject, "correct-claim", 1, h.now()); err != nil {
		t.Fatal(err)
	}
	if recovered, err := h.st.RequeueClaimedReports(ctx); err != nil || recovered != 0 {
		t.Fatalf("restart requeued consumed report: %d %v", recovered, err)
	}
}

func TestQueuedWorkerMessagesBatchAndKeepNewerFinalAndUserBoundary(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "long task", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	for _, text := range []string{"build blocked", "waiting on checks", "artifact build.zip; keep this output"} {
		h.advance(time.Second)
		receipt, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession, text, "", ports.MessageDeliveryOptions{SenderSessionID: string(worker.ID)})
		if err != nil || receipt.Outcome != ports.MessageQueued {
			t.Fatalf("worker receipt=%+v err=%v", receipt, err)
		}
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "human follow-up", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	h.advance(time.Minute)
	worker.IsTerminated = true
	if err := h.st.UpdateSession(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if err := h.st.RecordLastAssistantMessage(ctx, worker.ID, "release build succeeded", h.now()); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 2 })
	got := h.conv.sentTexts()[1]
	for _, want := range []string{"build blocked", "waiting on checks", "artifact build.zip", "release build succeeded", "terminated=true", "sent 2026"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "human follow-up") {
		t.Fatal("batch absorbed a user message")
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.Text != "human follow-up" {
		t.Fatalf("remaining queue=%+v err=%v", next, err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2"}, ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 3 })
	if h.conv.sentTexts()[2] != "human follow-up" {
		t.Fatalf("user delayed behind old status turns: %v", h.conv.sentTexts())
	}
}

func TestReportSendFailureRemainsPendingWithoutBlindResend(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	h.conv.sendErr = errors.New("provider receipt unavailable")
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	r, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, State: domain.ReportDone, Note: "final result"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	if _, err := coordinator.RunDue(ctx); err == nil {
		t.Fatal("expected send failure")
	}
	h.conv.mu.Lock()
	h.conv.sendErr = nil
	h.conv.mu.Unlock()
	h.advance(time.Minute)
	// Reconstructing the coordinator also exercises durable retry identity.
	coordinator = reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	if _, err := coordinator.RunDue(ctx); err == nil || !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("retry=%v", err)
	}
	got, _, err := h.st.GetReport(ctx, r.ID)
	if err != nil || got.DeliveryState != domain.ReportPending {
		t.Fatalf("report lost: %+v %v", got, err)
	}
	h.conv.mu.Lock()
	calls := h.conv.sendCalls
	h.conv.mu.Unlock()
	if calls != 2 { // the batch once, plus the one-off unconfirmed notice
		t.Fatalf("ambiguous delivery blindly resent: %d calls", calls)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(snapshot.Turns) != 2 || !strings.Contains(snapshot.Turns[0].ErrorMessage, "send a new Chat message") {
		t.Fatalf("recovery not visible: %+v %v", snapshot.Turns, err)
	}
	h.svc.SetReportCoordinator(coordinator)
	// Let the one-off unconfirmed notice turn finish so the user message dispatches.
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, turn := range s.Turns {
			if turn.ProviderTurnID == "provider-turn-1" && turn.State == domain.TurnStateCompleted {
				return true
			}
		}
		return false
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "I checked the provider; read the pending reports", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	got, _, err = h.st.GetReport(ctx, r.ID)
	if err != nil || got.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("explicit recovery failed: %+v %v", got, err)
	}
	if texts := h.conv.sentTexts(); len(texts) != 2 || !strings.Contains(texts[1], "final result") {
		t.Fatalf("recovered prompt=%v", texts)
	}
	if n, err := h.st.RequeueClaimedReports(ctx); err != nil || n != 0 {
		t.Fatalf("recovered report resurrected: %d %v", n, err)
	}
}

func TestCompactionSteerQueuesUntilSettlement(t *testing.T) {
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()
	if _, err := h.svc.Compact(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	result, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{Text: "steer during compaction", Origin: domain.MessageOriginHuman, ClientMessageID: "compact-steer"}, false)
	if err != nil || result.Steered || result.Turn.State != domain.TurnStateQueued {
		t.Fatalf("steer=%+v err=%v", result, err)
	}
	if got := conv.sentTexts(); len(got) != 0 {
		t.Fatalf("sent while compacting: %v", got)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventCompacted, ProviderTurnID: "compact-turn"})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(conv.sentTexts()) == 1 })
	if conv.sentTexts()[0] != "steer during compaction" {
		t.Fatal(conv.sentTexts())
	}
}

func TestCompactionSettlementReleasesBusyReport(t *testing.T) {
	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	h.svc.SetReportCoordinator(coordinator)
	if _, err := h.svc.Compact(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	r, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, Message: "result during compaction"})
	if err != nil {
		t.Fatal(err)
	}
	if delay, err := coordinator.RunDue(ctx); err != nil || delay != reportsvc.OrchestratorBusyRetry {
		t.Fatalf("delay=%v err=%v", delay, err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	stopped := coordinator.Start(runCtx)
	t.Cleanup(func() { cancel(); <-stopped })
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventCompacted, ProviderTurnID: "compact-turn"})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool {
		got, _, err := h.st.GetReport(ctx, r.ID)
		return err == nil && got.DeliveryState == domain.ReportAcknowledged
	})
	if len(conv.sentTexts()) != 1 {
		t.Fatal(conv.sentTexts())
	}
}

func TestIdleSignalOnlyExpeditesBusyRetries(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	for _, reason := range []string{domain.ReportDeliveryBusyReason, "provider unavailable", "repeated stuck report coalesced inside interrupt window"} {
		r, err := h.st.CreateReport(ctx, domain.ReportRecord{ID: reason, SessionID: worker.ID, ProjectID: testProject, Message: reason, CreatedAt: h.now(), AvailableAt: h.now(), DeliveryState: domain.ReportPending, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := h.st.ClaimPendingReportBatch(ctx, testProject, "", r.ID, h.now()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.st.DeferReportBatch(ctx, testProject, r.ID, h.now().Add(time.Minute), reason); err != nil {
			t.Fatal(err)
		}
	}
	// The restart coordinator must recognize durable reasons without an in-memory retry registry.
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	coordinator.Available(testSession, "retired-generation")
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	if len(h.conv.sentTexts()) != 0 {
		t.Fatal("stale generation released reports")
	}
	rec, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.Available(testSession, rec.Metadata.ControllerGeneration)
	coordinator.Available(testSession, rec.Metadata.ControllerGeneration)
	if _, err := coordinator.RunDue(ctx); err != nil {
		t.Fatal(err)
	}
	for _, reason := range []string{domain.ReportDeliveryBusyReason, "provider unavailable", "repeated stuck report coalesced inside interrupt window"} {
		r, _, err := h.st.GetReport(ctx, reason)
		if err != nil {
			t.Fatal(err)
		}
		if reason == domain.ReportDeliveryBusyReason {
			if r.DeliveryState != domain.ReportAcknowledged {
				t.Fatal(r)
			}
		} else if r.DeliveryState != domain.ReportPending || !r.AvailableAt.Equal(h.now().Add(time.Minute)) {
			t.Fatalf("backoff changed: %+v", r)
		}
	}
	if len(h.conv.sentTexts()) != 1 {
		t.Fatal(h.conv.sentTexts())
	}
}

func TestStoppedOrFailedCompletionDoesNotExpediteReports(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprint(stop), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			worker := seedReportWorker(t, h)
			coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
			h.svc.SetReportCoordinator(coordinator)
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", Origin: domain.MessageOriginHuman, ClientMessageID: "original-active"}); err != nil {
				t.Fatal(err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
			reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
			r, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, Message: "pending"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := coordinator.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			state := domain.TurnStateFailed
			if stop {
				if err := h.svc.Interrupt(ctx, testSession); err != nil {
					t.Fatal(err)
				}
				state = domain.TurnStateInterrupted
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: state})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 1 && s.Turns[0].State == state })
			// SubmitReports takes the same dispatch lock, so the completion hook has finished.
			_ = h.svc.SubmitReports(ctx, testSession, "active", "original-active")
			if _, err := coordinator.RunDue(ctx); err != nil {
				t.Fatal(err)
			}
			if len(h.conv.sentTexts()) != 1 {
				t.Fatal("completion started another turn")
			}
			got, _, err := h.st.GetReport(ctx, r.ID)
			if err != nil || got.DeliveryState != domain.ReportPending || !got.AvailableAt.After(h.now()) {
				t.Fatalf("expedited=%+v %v", got, err)
			}
		})
	}
}

func TestDeferredReportStartFailureIsNotAcknowledgedOnRetry(t *testing.T) {
	// Starts per provider turn: the batch turn and the unconfirmed-notice turn
	// are distinct turns and must each start exactly once.
	startsByTurn := map[string]int{}
	conv := &deferredConversation{fakeConversation: newFakeConversation(), start: func(providerTurnID string) error {
		startsByTurn[providerTurnID]++
		return errors.New("deferred request never started")
	}}
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()
	worker := seedReportWorker(t, h)
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	r, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: worker.ID, Message: "important result"})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := reportsvc.NewCoordinator(reportsvc.CoordinatorDeps{Store: h.st, Delivery: reportChatDelivery{h.svc}, Now: h.now})
	if _, err := coordinator.RunDue(ctx); err == nil {
		t.Fatal("expected failed deferred start")
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil || len(snapshot.Turns) != 1 || snapshot.Turns[0].ProviderTurnID == "" {
		t.Fatalf("expected prepared provider ID: %+v %v", snapshot.Turns, err)
	}
	h.advance(time.Minute)
	if _, err := coordinator.RunDue(ctx); err == nil || !strings.Contains(err.Error(), "explicit recovery") {
		t.Fatalf("retry=%v", err)
	}
	got, _, err := h.st.GetReport(ctx, r.ID)
	if err != nil || got.DeliveryState != domain.ReportPending {
		t.Fatalf("report=%+v err=%v", got, err)
	}
	if len(startsByTurn) != 2 {
		t.Fatalf("starts by turn=%v, want a batch turn and a notice turn", startsByTurn)
	}
	for turn, n := range startsByTurn {
		if n != 1 {
			t.Fatalf("turn %s started %d times, want 1 (starts=%v)", turn, n, startsByTurn)
		}
	}
}

func TestReportBatchRetriesUnderDerivedKeyWhenHostWasNotRunning(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.conv.sendErr = ports.ErrChatTurnNotAccepted
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:x"); err == nil {
		t.Fatal("expected first dispatch to fail")
	}
	h.conv.sendErr = nil
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:x"); err != nil {
		t.Fatalf("retry after definitive not-running failure = %v", err)
	}
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:x"); err != nil {
		t.Fatalf("idempotent re-submit = %v", err)
	}
	if got := len(h.conv.sentTexts()); got != 1 {
		t.Fatalf("sent %d prompts, want exactly 1", got)
	}
}

func TestReportBatchAmbiguousFailureIsNeverResent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.conv.sendErr = errors.New("connection reset")
	_ = h.svc.SubmitReports(ctx, testSession, "report", "report-batch:y")
	h.conv.sendErr = nil
	err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:y")
	if !errors.Is(err, reportsvc.ErrDeliveryUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeliveryUnconfirmed", err)
	}
	if len(h.conv.sentTexts()) != 0 {
		t.Fatal("ambiguous batch was resent")
	}
}

func TestReportBatchEndsUnconfirmedAfterDerivedKeysExhausted(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.conv.sendErr = ports.ErrChatTurnNotAccepted
	var err error
	for i := 0; i < 10; i++ {
		err = h.svc.SubmitReports(ctx, testSession, "report", "report-batch:z")
		if errors.Is(err, reportsvc.ErrDeliveryUnconfirmed) {
			break
		}
	}
	if !errors.Is(err, reportsvc.ErrDeliveryUnconfirmed) {
		t.Fatalf("err = %v, want ErrDeliveryUnconfirmed once derived keys are exhausted", err)
	}
	h.conv.sendErr = nil
	if err := h.svc.SubmitReports(ctx, testSession, "report", "report-batch:z"); !errors.Is(err, reportsvc.ErrDeliveryUnconfirmed) {
		t.Fatalf("after exhaustion err = %v, want it to stay unconfirmed", err)
	}
	if len(h.conv.sentTexts()) != 0 {
		t.Fatal("exhausted batch was sent")
	}
}
