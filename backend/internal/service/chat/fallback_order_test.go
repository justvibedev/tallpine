package chat_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func TestReorderedQueueFallbackFollowsAcceptedPrimary(t *testing.T) {
	for _, state := range []domain.ReportState{"", domain.ReportDone, domain.ReportNeedsInput, domain.ReportStuck, domain.ReportCheckpoint} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			ctx := context.Background()
			acks := make(chan string, 32)
			provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
			h := newHarnessWithConversation(t, provider)
			makeFallbackWorker(t, h.st)
			send := func(text string) domain.ConversationTurn {
				t.Helper()
				turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: text, ClientMessageID: domain.WorkerSendClientMessagePrefix + text, Origin: domain.MessageOriginAutomation})
				if err != nil {
					t.Fatal(err)
				}
				return turn
			}
			send("busy")
			h.advance(time.Second)
			a := send("A")
			h.advance(time.Second)
			b := send("B")
			if a.State != domain.TurnStateQueued || b.State != domain.TurnStateQueued {
				t.Fatal("work was not queued")
			}
			if err := h.svc.ReorderQueuedTurns(ctx, testSession, []string{b.ID, a.ID}); err != nil {
				t.Fatal(err)
			}
			// The executions and completions deliberately share one fake clock value.
			finish := func(id, text string) {
				t.Helper()
				provider.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: id, ProviderItemID: id + "-answer", ProviderEventID: id + "-answer", Text: text},
					ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: id, ProviderEventID: id + "-end", TurnState: domain.TurnStateCompleted})
				awaitFallbackReceipt(t, acks, id+"-end")
			}
			finish("provider-turn-1", "busy final")
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return turnStateByText(t, s)["B"] == domain.TurnStateRunning })
			finish("provider-turn-2", "B final")
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return turnStateByText(t, s)["A"] == domain.TurnStateRunning })
			start := ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-3", ProviderEventID: "A-start"}
			provider.emit(start)
			awaitFallbackReceipt(t, acks, "A-start")
			finish("provider-turn-3", "A final")
			if got := provider.sentTexts(); len(got) != 3 || got[1] != "B" || got[2] != "A" {
				t.Fatalf("execution=%v", got)
			}
			// Even a repeated old start with a fresh receipt cannot reclaim ownership.
			provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2", ProviderEventID: "B-repeat"})
			awaitFallbackReceipt(t, acks, "B-repeat")
			if state != "" {
				reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
				if _, err := reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: state, Note: "A explicit"}); err != nil {
					t.Fatal(err)
				}
			}
			dispositions := fallbackDispositions(t, h.st)
			want := "pending"
			if state != "" && state != domain.ReportCheckpoint {
				want = "suppressed"
			}
			if dispositions[a.ID] != want || dispositions[b.ID] != "superseded" {
				t.Fatalf("owners=%v; A=%s B=superseded", dispositions, want)
			}
			h.svc.StopAll(ctx)
			h.ctrl.Wait()
			project, _, err := h.st.GetProject(ctx, string(testProject))
			if err != nil {
				t.Fatal(err)
			}
			if err = h.st.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sqlite.OpenPreMigrated(project.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			pending, err := reopened.ListPendingTurnFallbacks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if want == "pending" {
				if len(pending) != 1 || pending[0].TurnID != a.ID || pending[0].Excerpt != "A final" {
					t.Fatalf("recovered=%+v", pending)
				}
			} else if len(pending) != 0 {
				t.Fatalf("explicit report lost suppression: %+v", pending)
			}
			h.advance(domain.TurnFallbackDelay)
			reports := reportsvc.New(reportsvc.Deps{Store: reopened, Now: h.now})
			id := (domain.TurnFallback{SessionID: testSession, Mode: domain.SessionModeChat, TurnID: a.ID}).StableID()
			_, err = reports.Create(ctx, reportsvc.CreateInput{ID: id, TurnFallbackID: id, SessionID: testSession, Synthesized: true, Message: "A final"})
			if want == "pending" && err != nil {
				t.Fatalf("A final lost: %v", err)
			}
			if want == "suppressed" && !errors.Is(err, domain.ErrTurnFallbackInactive) {
				t.Fatalf("A suppression lost: %v", err)
			}
		})
	}
}

func TestQueuedWorkAfterJournalAdoptionOwnsFallback(t *testing.T) {
	ctx := context.Background()
	acks := make(chan string, 32)
	provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
	h := newHarnessWithConversation(t, provider)
	makeFallbackWorker(t, h.st)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "busy", ClientMessageID: "busy", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued", ClientMessageID: "queued", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderEventID: "failed", TurnState: domain.TurnStateFailed})
	awaitFallbackReceipt(t, acks, "failed")
	// Failure holds the pre-existing queue. A live journal start now adopts a
	// later-inserted primary; its successful completion drains the older row.
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "journal", ProviderEventID: "journal-start"})
	awaitFallbackReceipt(t, acks, "journal-start")
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "journal", ProviderEventID: "journal-end", TurnState: domain.TurnStateCompleted})
	awaitFallbackReceipt(t, acks, "journal-end")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued"] == domain.TurnStateRunning
	})
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2", ProviderEventID: "queued-end", TurnState: domain.TurnStateCompleted})
	awaitFallbackReceipt(t, acks, "queued-end")
	pending, err := h.st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 || pending[0].TurnID != queued.ID {
		t.Fatalf("accepted queued result=%+v %v", pending, err)
	}
}

// A queued follow-up is intake, not a start: it must not retire the running
// turn's fallback, so a failure that holds the queue still produces a notice.
func TestQueuedIntakeKeepsRunningTurnFallback(t *testing.T) {
	for _, scenario := range []string{"fail", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			acks := make(chan string, 16)
			provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
			h := newHarnessWithConversation(t, provider)
			makeFallbackWorker(t, h.st)
			turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "primary", ClientMessageID: "primary", Origin: domain.MessageOriginAutomation})
			if err != nil {
				t.Fatal(err)
			}
			queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "new queued work", ClientMessageID: "new", Origin: domain.MessageOriginAutomation})
			if err != nil {
				t.Fatal(err)
			}
			end := ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderEventID: "end", TurnState: domain.TurnStateFailed}
			if scenario == "cancel" {
				if err = h.svc.CancelQueuedTurn(ctx, testSession, queued.ID); err != nil {
					t.Fatal(err)
				}
				end.TurnState = domain.TurnStateCompleted
			}
			provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderEventID: "duplicate-start"}, end)
			awaitFallbackReceipt(t, acks, "end")
			if got := fallbackDispositions(t, h.st)[turn.ID]; got != "pending" {
				t.Fatalf("running turn's result lost: %s", got)
			}
			if got, err := h.st.HasCurrentCompletedTurnFallback(ctx, testSession); err != nil || !got {
				t.Fatalf("result not owned: %v %v", got, err)
			}
		})
	}
}

// A pending failure notice survives held-queue intake; once delivered, the
// next intake retires it.
func TestQueuedIntakeRetiresDeliveredFallbackOnly(t *testing.T) {
	ctx := context.Background()
	acks := make(chan string, 16)
	provider := &receiptConversation{terminatingConversation: &terminatingConversation{fakeConversation: newFakeConversation()}, ack: func(id string) error { acks <- id; return nil }}
	h := newHarnessWithConversation(t, provider)
	makeFallbackWorker(t, h.st)
	a, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "A", ClientMessageID: "A", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	b0, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "B", ClientMessageID: "B", Origin: domain.MessageOriginAutomation})
	if err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderEventID: "end", TurnState: domain.TurnStateFailed})
	awaitFallbackReceipt(t, acks, "end")
	// Queue is held after the failure; B is intake only. A's notice is still pending.
	if got := fallbackDispositions(t, h.st)[a.ID]; got != "pending" {
		t.Fatalf("failure notice dropped by held-queue intake: %s", got)
	}
	h.advance(domain.TurnFallbackDelay)
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	id := (domain.TurnFallback{SessionID: testSession, Mode: domain.SessionModeChat, TurnID: a.ID}).StableID()
	if _, err = reports.Create(ctx, reportsvc.CreateInput{ID: id, TurnFallbackID: id, SessionID: testSession, Synthesized: true, Message: "A failed"}); err != nil {
		t.Fatalf("failure notice not emitted: %v", err)
	}
	// Once emitted, the next intake retires it.
	if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "C", ClientMessageID: "C", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	if got := fallbackDispositions(t, h.st)[a.ID]; got != "superseded" {
		t.Fatalf("emitted result survived new work: %s", got)
	}
	_ = b0
}

// Promoting a queued follow-up into the running turn must not strand the
// running turn's result: exactly one notice is created for it.
func TestPromotedQueuedTurnKeepsRunningTurnNoticeOnce(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	makeFallbackWorker(t, h.st)
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "steer me", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderItemID: "a", ProviderEventID: "a", Text: "A final"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderEventID: "end", TurnState: domain.TurnStateCompleted})
	var pending []domain.TurnFallback
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pending, err = h.st.ListPendingTurnFallbacks(ctx); err != nil || len(pending) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || len(pending) != 1 || pending[0].Excerpt != "A final" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	h.advance(domain.TurnFallbackDelay)
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	id := pending[0].StableID()
	in := reportsvc.CreateInput{ID: id, TurnFallbackID: id, SessionID: testSession, Synthesized: true, Message: "A final"}
	if _, err = reports.Create(ctx, in); err != nil {
		t.Fatalf("first emit: %v", err)
	}
	if _, err = reports.Create(ctx, in); err == nil {
		t.Fatal("notice emitted twice")
	}
	if rows, err := h.st.ListPendingTurnFallbacks(ctx); err != nil || len(rows) != 0 {
		t.Fatalf("still pending: %+v %v", rows, err)
	}
}
