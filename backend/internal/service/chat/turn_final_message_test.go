package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// A completed root turn carries its final assistant message on the lifecycle
// signal, so the turn observer can quote it in a synthesized worker report.
// Nested-thread text and text from an earlier turn never answer it.
func TestTurnCompletedSignalCarriesFinalAssistantMessage(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	worker, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("worker fixture: found=%v err=%v", found, err)
	}
	worker.Kind = domain.KindWorker
	if err = h.st.UpdateSession(ctx, worker); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "fix it", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["fix it"] == domain.TurnStateRunning
	})
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m1", Delta: "looking"},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m1"},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m2", Delta: "Fixed "},
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m2", Delta: "the bug."},
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "child-1", ProviderConversationID: "child-thread"},
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "child-1", ProviderConversationID: "child-thread", ProviderItemID: "child-m", Text: "child chatter"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "child-1", ProviderConversationID: "child-thread", TurnState: domain.TurnStateCompleted},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["fix it"] == domain.TurnStateCompleted
	})
	completed := awaitTurnCompletedSignal(t, h.activity)
	if completed.FinalAssistantMessage != "Fixed the bug." {
		t.Fatalf("final assistant message = %q, want %q", completed.FinalAssistantMessage, "Fixed the bug.")
	}
	pending, err := h.st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 || pending[0].Excerpt != "Fixed the bug." {
		t.Fatalf("committed primary fallback = %+v, err=%v", pending, err)
	}
	if completed.ControllerGeneration == "" || completed.State != domain.ActivityIdle {
		t.Fatalf("completed signal = %+v", completed)
	}
}

func awaitTurnCompletedSignal(t *testing.T, activity *recordingActivity) ports.ActivitySignal {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, signal := range activity.snapshot() {
			if signal.Event == "chat.turn.completed" {
				return signal
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no chat.turn.completed signal: %+v", activity.snapshot())
	return ports.ActivitySignal{}
}

func TestFailedPrimaryTurnNotifiesWhileControllerStaysAlive(t *testing.T) {
	for _, tc := range []struct {
		name               string
		state              domain.TurnState
		err                error
		eligible, reported bool
	}{
		{name: "provider error", state: domain.TurnStateFailed, err: errors.New("provider unavailable"), eligible: true},
		{name: "failure without error", state: domain.TurnStateFailed, eligible: true},
		{name: "error overrides success", state: domain.TurnStateCompleted, err: errors.New("usage limit exhausted"), eligible: true},
		{name: "explicit report after failure", state: domain.TurnStateFailed, err: errors.New("provider unavailable"), eligible: true, reported: true},
		{name: "authentication wait", state: domain.TurnStateFailed, err: ports.ErrChatAuthRequired},
		{name: "intentional interruption", state: domain.TurnStateInterrupted, err: errors.New("cancelled")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			worker, found, err := h.st.GetSession(ctx, testSession)
			if err != nil || !found {
				t.Fatalf("worker fixture: found=%v err=%v", found, err)
			}
			worker.Kind = domain.KindWorker
			if err = h.st.UpdateSession(ctx, worker); err != nil {
				t.Fatal(err)
			}
			if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "attempt work", ClientMessageID: "failed-turn", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatal(err)
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1"})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return turnStateByText(t, s)["attempt work"] == domain.TurnStateRunning
			})
			h.conv.emit(
				ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "result", Text: "Unable to finish this attempt."},
				ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", TurnState: tc.state, Err: tc.err},
			)
			expectedState := domain.TurnStateFailed
			if tc.state == domain.TurnStateInterrupted {
				expectedState = domain.TurnStateInterrupted
			}
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return turnStateByText(t, s)["attempt work"] == expectedState })
			if tc.eligible {
				awaitTurnCompletedSignal(t, h.activity)
			}
			pending, err := h.st.ListPendingTurnFallbacks(ctx)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.eligible {
				want = 1
			}
			if len(pending) != want {
				t.Fatalf("pending=%+v, want %d durable fallback(s)", pending, want)
			}
			if !tc.eligible {
				return
			}
			if pending[0].Excerpt != "Unable to finish this attempt." {
				t.Fatalf("excerpt=%q", pending[0].Excerpt)
			}
			reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
			if tc.reported {
				if _, err = reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: domain.ReportNeedsInput, Note: "Provider failed; help needed."}); err != nil {
					t.Fatal(err)
				}
				pending, err = h.st.ListPendingTurnFallbacks(ctx)
				if err != nil || len(pending) != 0 {
					t.Fatalf("explicit failure report did not suppress fallback: %+v, %v", pending, err)
				}
				return
			}
			h.advance(domain.TurnFallbackDelay)
			observer := turnnotify.New(turnnotify.Deps{Next: h.activity, Sessions: h.st, Fallbacks: h.st, Reports: reports, Now: h.now})
			deliveryCtx, cancel := context.WithCancel(ctx)
			done := observer.Start(deliveryCtx)
			defer func() { cancel(); observer.Close(); <-done }()
			deadline := time.Now().Add(5 * time.Second)
			for {
				rows, readErr := h.st.ListReportsByProject(ctx, testProject)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if len(rows) == 1 {
					if rows[0].ID != pending[0].ID {
						t.Fatalf("report ID=%q want %q", rows[0].ID, pending[0].ID)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("failed turn produced %d reports, want 1", len(rows))
				}
				time.Sleep(10 * time.Millisecond)
			}
			if state := h.ctrl.State(); state != ports.ChatControllerReady {
				t.Fatalf("controller=%s, want ready without an exit", state)
			}
		})
	}
}

func TestLiveReconnectedPrimaryReportSuppressesCompletionDurably(t *testing.T) {
	testLiveReconnectedPrimaryCompletion(t, true)
}

func TestLiveReconnectedUnreportedPrimaryCompletionRemainsEligible(t *testing.T) {
	testLiveReconnectedPrimaryCompletion(t, false)
}

func testLiveReconnectedPrimaryCompletion(t *testing.T, reported bool) {
	t.Helper()
	st := openStore(t)
	ctx := context.Background()
	rec, _, err := st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Kind = domain.KindWorker
	if err = st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var ids atomic.Int32
	newID := func() string { return fmt.Sprintf("reclaim-%d", ids.Add(1)) }
	firstProvider := &terminatingConversation{fakeConversation: newFakeConversation()}
	first := chatsvc.New(chatsvc.Options{Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: firstProvider}}, Log: slog.New(slog.DiscardHandler), NewID: newID})
	firstController, err := first.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex, WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { first.StopAll(ctx) })
	turn, err := firstController.Send(ctx, ports.ChatUserMessage{Text: "surviving work"})
	if err != nil {
		t.Fatal(err)
	}
	firstProvider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID, ProviderConversationID: firstProvider.ProviderConversationID()})
	h := &harness{st: st, ctrl: firstController}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["surviving work"] == domain.TurnStateRunning
	})
	first.StopAll(ctx)
	rec, _, err = st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	rec.Metadata.ProviderConversationID = firstProvider.ProviderConversationID()
	if err = st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	secondProvider := &liveReconnectedConversation{nativeHistoryConversation: &nativeHistoryConversation{fakeConversation: newFakeConversation()}}
	second := chatsvc.New(chatsvc.Options{Store: st, Reader: fullSnapshotReader(st), Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: secondProvider}}, Log: slog.New(slog.DiscardHandler), NewID: newID})
	t.Cleanup(func() { second.StopAll(ctx) })
	secondController, err := second.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: firstProvider.ProviderConversationID()})
	if err != nil {
		t.Fatal(err)
	}
	if secondController.State() != ports.ChatControllerBusy {
		t.Fatal("running turn not reclaimed")
	}
	if reported {
		reports := reportsvc.New(reportsvc.Deps{Store: st}) // deliberately no ReportFiled callback
		if _, err = reports.Create(ctx, reportsvc.CreateInput{SessionID: testSession, State: domain.ReportDone, Note: "reported after reconnect"}); err != nil {
			t.Fatal(err)
		}
	}
	secondProvider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		ProviderConversationID: secondProvider.ProviderConversationID(), TurnState: domain.TurnStateCompleted})
	h.ctrl = secondController
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["surviving work"] == domain.TurnStateCompleted
	})
	want := 1
	if reported {
		want = 0
	}
	if pending, err := st.ListPendingTurnFallbacks(ctx); err != nil || len(pending) != want {
		t.Fatalf("reclaimed completion pending=%+v err=%v", pending, err)
	}
}
