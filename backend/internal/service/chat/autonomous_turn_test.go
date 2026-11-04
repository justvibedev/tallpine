package chat_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func activityEvents(h *harness) []string {
	var out []string
	for _, s := range h.activity.snapshot() {
		out = append(out, s.Event)
	}
	return out
}

// B440: a turn Claude Code starts by itself when a background command ends has
// no provider turn events, only a "turn" async task. It must open and close the
// activity turn like a prompted one, so turnnotify reports it to the orchestrator.
func TestAutonomousTurnTaskDrivesTurnLifecycleSignals(t *testing.T) {
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
	if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "fix it", ClientMessageID: "c1", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["fix it"] == domain.TurnStateRunning
	})
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m0", Delta: "ok"},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["fix it"] == domain.TurnStateCompleted
	})
	awaitTurnCompletedSignal(t, h.activity)
	before := len(h.activity.snapshot())

	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{
		ID: "autonomous-turn-1", TaskType: "turn", State: ports.ChatAsyncTaskRunning}})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m1", Delta: "Lint passed on both trees."})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{
		ID: "autonomous-turn-1", State: ports.ChatAsyncTaskCompleted}})

	// Only signals after the prompted turn count.
	deadline := time.Now().Add(5 * time.Second)
	var after []ports.ActivitySignal
	for time.Now().Before(deadline) {
		if all := h.activity.snapshot(); len(all) >= before+2 {
			after = all[before:]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(after) < 2 {
		t.Fatalf("autonomous turn produced no lifecycle signals: %v", activityEvents(h))
	}
	if after[0].Event != "chat.turn.started" || after[0].State != domain.ActivityActive {
		t.Fatalf("first signal = %+v, want chat.turn.started active", after[0])
	}
	done := after[1]
	if done.Event != "chat.turn.completed" || done.State != domain.ActivityIdle ||
		done.FinalAssistantMessage != "Lint passed on both trees." || done.ControllerGeneration == "" {
		t.Fatalf("second signal = %+v, want chat.turn.completed idle with the final message", done)
	}
}

// Other async tasks (a background Bash command) are not turns.
func TestBackgroundCommandTaskSendsNoTurnSignals(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{
		ID: "b1", TaskType: "local_bash", State: ports.ChatAsyncTaskRunning}})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{
		ID: "b1", State: ports.ChatAsyncTaskCompleted}})
	time.Sleep(300 * time.Millisecond)
	for _, e := range activityEvents(h) {
		if e == "chat.turn.started" || e == "chat.turn.completed" {
			t.Fatalf("background command produced %s", e)
		}
	}
}

type noopApplier struct{}

func (noopApplier) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}

// B440 end to end: the previous prompted turn's fallback row is already
// emitted (the worker said "I'll report when it finishes"). The autonomous turn
// that answers the finished command must still produce a turn_ended report with
// its own final message, from a real turnnotify.Observer against the real store.
func TestAutonomousTurnAfterEmittedFallbackFilesTurnEndedReport(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	worker, _, err := h.st.GetSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	worker.Kind = domain.KindWorker
	if err = h.st.UpdateSession(ctx, worker); err != nil {
		t.Fatal(err)
	}
	reports := reportsvc.New(reportsvc.Deps{Store: h.st, Now: h.now})
	observer := turnnotify.New(turnnotify.Deps{Next: noopApplier{}, Sessions: h.st, Fallbacks: h.st, Reports: reports, Now: h.now,
		IdleDelay: 20 * time.Millisecond})
	h.activity.forward = observer.ApplyActivitySignal
	deliveryCtx, cancel := context.WithCancel(ctx)
	done := observer.Start(deliveryCtx)
	defer func() { cancel(); observer.Close(); <-done }()

	if _, err = h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "fix it", ClientMessageID: "c1", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["fix it"] == domain.TurnStateRunning
	})
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", ProviderItemID: "m1", Text: "Started the build; I will report when it finishes."},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted},
	)
	awaitTurnCompletedSignal(t, h.activity)
	h.advance(domain.TurnFallbackDelay)
	awaitReports := func(n int) []domain.ReportRecord {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			rows, readErr := h.st.ListReportsByProject(ctx, testProject)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if len(rows) >= n {
				return rows
			}
			if time.Now().After(deadline) {
				t.Fatalf("got %d reports, want %d", len(rows), n)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	awaitReports(1) // the prompted turn's row is now emitted

	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{ID: "auto-1", TaskType: "turn", State: ports.ChatAsyncTaskRunning}})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderItemID: "m2", Delta: "Lint passed on both trees."})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventAsyncTask, AsyncTask: &ports.ChatAsyncTask{ID: "auto-1", State: ports.ChatAsyncTaskCompleted}})
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, _ := h.st.ListReportsByProject(ctx, testProject)
		for _, r := range rows {
			if strings.Contains(r.Message, "Lint passed on both trees.") {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no turn_ended report with the autonomous turn's message: %+v", rows)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
