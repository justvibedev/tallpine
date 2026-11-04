package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// The composer tells the user a mid-turn message is queued until the agent
// finishes. That has to be true of the daemon, not just of the placeholder: a
// second turn/start against a busy provider is not a thing the agent can run.
func TestSendWhileBusyQueuesUntilTheTurnEnds(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "second", ClientMessageID: "c2", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("mid-turn Send: %v", err)
	}
	if queued.State != domain.TurnStateQueued {
		t.Errorf("mid-turn send reported state %q, want queued", queued.State)
	}
	if queued.ProviderTurnID != "" {
		t.Errorf("mid-turn send claimed provider turn %q; it was never dispatched", queued.ProviderTurnID)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; the second message must wait", got)
	}

	// The running turn ends, so the queued message goes out on its own.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["second"] == domain.TurnStateRunning
	})
	states := turnStateByText(t, snapshot)
	if states["first"] != domain.TurnStateCompleted {
		t.Errorf("first turn = %q, want completed", states["first"])
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "second" {
		t.Fatalf("provider received %v, want the queued message dispatched second", got)
	}
}

// Codex can start nested turns while the root turn is still working. A child
// completion is not conversation quiescence: dispatching queued automation at
// that point injects it into the still-running root and leaves the Tallpine turn minted
// for that automation with no matching provider lifecycle.
func TestNestedTurnCompletionDoesNotDrainQueueWhilePrimaryTurnRuns(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root work", ClientMessageID: "c1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
		ProviderConversationID: "thread-1",
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root work"] == domain.TurnStateRunning
	})

	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued automation", ClientMessageID: "c2", Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatalf("queue automation: %v", err)
	}
	if queued.State != domain.TurnStateQueued {
		t.Fatalf("automation state = %q, want queued", queued.State)
	}

	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-child-1",
			ProviderConversationID: "child-thread-1",
		},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-child-1",
			ProviderConversationID: "child-thread-1", TurnState: domain.TurnStateCompleted,
		},
		// This later root event is a deterministic barrier: once projected, the
		// child's afterProject hook (including any incorrect drain) has finished.
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "root-still-working", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "root continued",
		},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.ProviderItemID == "root-still-working" {
				return true
			}
		}
		return false
	})
	states := turnStateByText(t, snapshot)
	if states["queued automation"] != domain.TurnStateQueued {
		t.Errorf("automation state after child completion = %q, want queued", states["queued automation"])
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; child completion must not drain queued automation", got)
	}
	if got := h.ctrl.State(); got != ports.ChatControllerBusy {
		t.Errorf("controller state after child completion = %q, want busy", got)
	}
	for _, signal := range h.activity.snapshot() {
		if signal.State == domain.ActivityIdle {
			t.Fatalf("child completion reported the session idle: %+v", signal)
		}
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued automation"] == domain.TurnStateRunning
	})
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "queued automation" {
		t.Fatalf("provider received %v, want automation only after root completion", got)
	}
	idleReported := false
	for _, signal := range h.activity.snapshot() {
		idleReported = idleReported || signal.State == domain.ActivityIdle
	}
	if !idleReported {
		t.Fatal("root completion did not report the session idle")
	}
}

// A Chat -> TUI handoff waits for the root turn and everything already queued
// behind it. Nested Codex lifecycle must not make that drain look complete, nor
// may it release the accepted queue into a root turn that is still running.
func TestChatHandoffDrainWaitsForRootAfterNestedTurnCompletes(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "root work", ClientMessageID: "handoff-nested-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("send root turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1",
		ProviderConversationID: "thread-1",
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["root work"] == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "accepted queue", ClientMessageID: "handoff-nested-2", Origin: domain.MessageOriginAutomation,
	}); err != nil {
		t.Fatalf("queue accepted turn: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionDrain)
	}()

	h.conv.emit(
		ports.ChatEvent{
			Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-child-1",
			ProviderConversationID: "child-thread-1",
		},
		ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-child-1",
			ProviderConversationID: "child-thread-1", TurnState: domain.TurnStateCompleted,
		},
		ports.ChatEvent{
			Kind: ports.ChatEventActivityCompleted, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "handoff-root-still-working", ActivityKind: domain.ActivityKindCommand,
			ActivityStatus: domain.ActivityStatusCompleted, Summary: "root continued",
		},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.ProviderItemID == "handoff-root-still-working" {
				return true
			}
		}
		return false
	})

	select {
	case err := <-done:
		t.Fatalf("handoff finished after child completion while root was active: %v", err)
	default:
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; nested completion released the accepted queue", got)
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["accepted queue"] == domain.TurnStateRunning
	})
	select {
	case err := <-done:
		t.Fatalf("handoff finished before its accepted queue completed: %v", err)
	default:
	}

	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2",
		ProviderConversationID: "thread-1", TurnState: domain.TurnStateCompleted,
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prepare handoff: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handoff did not become quiescent after the root and accepted queue completed")
	}
}

// Stop is a brake. Releasing the queue when the user presses it would be the
// opposite of what the button says, so anything waiting is cancelled with the turn.
func TestInterruptCancelsWhatIsQueuedBehindTheTurn(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("mid-turn Send: %v", err)
	}

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["queued"].Terminal() && states["running"].Terminal()
	})
	states := turnStateByText(t, snapshot)
	if states["queued"] != domain.TurnStateInterrupted {
		t.Errorf("queued turn = %q; a message never dispatched did not fail, it was cancelled",
			states["queued"])
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; stop must not release the queue", got)
	}
}

func TestChatHandoffDrainFinishesAcceptedQueueAndClosesNewIntake(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "handoff-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "already queued", ClientMessageID: "handoff-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionDrain)
	}()
	// Completion of the first accepted turn must dispatch the accepted queue,
	// rather than declaring the source quiescent early.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["already queued"] == domain.TurnStateRunning
	})
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-2",
		TurnState: domain.TurnStateCompleted,
	})
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prepare handoff: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("handoff did not become quiescent after its accepted queue completed")
	}

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "too late", ClientMessageID: "handoff-3",
	}); !errors.Is(err, chatsvc.ErrControllerHandoff) {
		t.Fatalf("send after handoff gate = %v, want ErrControllerHandoff", err)
	}
	h.svc.AbortChatHandoff(testSession)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "source reopened", ClientMessageID: "handoff-4",
	}); err != nil {
		t.Fatalf("send after aborting handoff: %v", err)
	}
}

func TestChatHandoffInterruptArmBlocksCompletionFromPromotingQueue(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the dev server", ClientMessageID: "handoff-arm-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "touch /tmp/tp3945-queued-ran", ClientMessageID: "handoff-arm-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}
	// Once intake is fenced, eventual cancellation must cover the whole queue
	// rather than trusting wall-clock ordering. A host clock correction cannot
	// make an older accepted command look newer than the handoff cutoff.
	h.advance(-time.Hour)

	// Session Manager performs this fast step before returning the accepted
	// transition and before target preflight. It is a reversible dispatch fence:
	// the active turn can finish, but its completion must not release the queue.
	if err := h.svc.ArmChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("arm interrupt handoff: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})
	fenced := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["run the dev server"].Terminal()
	})
	if got := turnStateByText(t, fenced)["touch /tmp/tp3945-queued-ran"]; got != domain.TurnStateQueued {
		t.Fatalf("queued command while target preflights = %q, want reversibly fenced", got)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v before handoff preparation; armed completion released the queue", got)
	}

	// Target preflight has now succeeded. Preparation makes the fence durable
	// before touching the provider and remains independent of clock movement.
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("prepare armed handoff: %v", err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("stop source controller: %v", err)
	}

	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; the queued command crossed the armed handoff", got)
	}
	snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load stopped conversation: %v", err)
	}
	if got := turnStateByText(t, snapshot)["touch /tmp/tp3945-queued-ran"]; got != domain.TurnStateInterrupted {
		t.Fatalf("queued command = %q, want interrupted without provider dispatch", got)
	}
}

func TestChatHandoffInterruptAbortAfterPreflightFailureResumesFencedQueue(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the dev server", ClientMessageID: "handoff-abort-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued work survives preflight", ClientMessageID: "handoff-abort-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}
	if err := h.svc.ArmChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("arm interrupt handoff: %v", err)
	}

	// Completion while target preflight is running cannot dispatch the queue.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["run the dev server"] == domain.TurnStateCompleted &&
			states["queued work survives preflight"] == domain.TurnStateQueued
	})
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v while target preflight was fenced", got)
	}

	// A failed target preflight reopens the source and explicitly resumes the
	// queue; there may be no later completion event to trigger this drain.
	h.svc.AbortChatHandoff(testSession)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued work survives preflight"] == domain.TurnStateRunning
	})
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "queued work survives preflight" {
		t.Fatalf("provider received %v after abort, want preserved queue to resume", got)
	}
}

func TestChatHandoffInterruptCompletionDuringProviderCancellationCannotPromoteQueue(t *testing.T) {
	t.Parallel()

	provider := &completionOnInterruptConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the dev server", ClientMessageID: "handoff-during-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "touch /tmp/tp3945-queued-ran", ClientMessageID: "handoff-during-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}

	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("prepare interrupt handoff: %v", err)
	}
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("stop source controller: %v", err)
	}
	if got := provider.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; ACP-style completion promoted the queue", got)
	}
}

func TestChatHandoffInterruptDoesNotWaitForTurnCompletion(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the dev server", ClientMessageID: "handoff-interrupt-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued behind it", ClientMessageID: "handoff-interrupt-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prepare interrupt handoff: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		// Unblock the old implementation before failing so the test leaves no
		// handoff goroutine behind. A force switch must not need this completion.
		h.conv.emit(ports.ChatEvent{
			Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
			TurnState: domain.TurnStateInterrupted,
		})
		<-done
		t.Fatal("interrupt handoff waited for the running turn to report completion")
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued behind it"].Terminal()
	})
	if got := turnStateByText(t, snapshot)["queued behind it"]; got != domain.TurnStateInterrupted {
		t.Fatalf("queued turn = %q, want interrupted before the source controller stops", got)
	}
	// Codex may deliver turn/completed after turn/interrupt has already answered.
	// The armed dispatch gate must still make that late completion a no-op for the
	// queue.
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatalf("stop armed controller: %v", err)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; late completion promoted an interrupted queue", got)
	}
}

func TestChatHandoffInterruptDoesNotReachProviderUntilQueueCancellationSucceeds(t *testing.T) {
	t.Parallel()

	provider := newInterruptRecorder()
	var flakyStore *failFirstQueueCancellationStore
	h := newHarnessWithConversationAndStore(t, provider, func(st *sqlite.Store) chatsvc.Store {
		flakyStore = &failFirstQueueCancellationStore{Store: st}
		return flakyStore
	})
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "run the dev server", ClientMessageID: "handoff-race-1",
	}); err != nil {
		t.Fatalf("send running turn: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	provider.markActive("provider-turn-1")
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued behind it", ClientMessageID: "handoff-race-2",
	}); err != nil {
		t.Fatalf("queue second turn: %v", err)
	}

	if err := h.svc.ArmChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("arm reversible handoff fence: %v", err)
	}
	if got := flakyStore.callCount(); got != 0 {
		t.Fatalf("queue cancellation calls during reversible arm = %d, want 0", got)
	}
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err == nil {
		t.Fatal("first handoff preparation error = nil, want injected queue cancellation failure")
	}
	if got := provider.attemptCount(); got != 0 {
		t.Fatalf("provider interrupt attempts = %d before durable queue cancellation, want 0", got)
	}

	// Preparation aborts the reversible fence after a local-store failure. A
	// caller may re-arm and retry; provider I/O begins only after the durable queue
	// is terminal.
	if err := h.svc.ArmChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("re-arm handoff after preparation failure: %v", err)
	}
	if err := h.svc.PrepareChatHandoff(ctx, testSession, domain.SessionInterfaceTransitionInterrupt); err != nil {
		t.Fatalf("prepare armed interrupt handoff: %v", err)
	}
	if got := flakyStore.callCount(); got != 2 {
		t.Fatalf("queue cancellation calls = %d, want one failed arm and one successful retry", got)
	}
	if got := provider.attemptCount(); got != 1 {
		t.Fatalf("provider interrupt attempts = %d, want 1 after queue cancellation", got)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["queued behind it"].Terminal()
	})
	if got := turnStateByText(t, snapshot)["queued behind it"]; got != domain.TurnStateInterrupted {
		t.Fatalf("queued turn = %q, want interrupted before provider cancellation", got)
	}
}

func TestChatHandoffTreatsMissingControllerAsAlreadyQuiescent(t *testing.T) {
	t.Parallel()

	svc := chatsvc.New(chatsvc.Options{})
	if err := svc.PrepareChatHandoff(
		context.Background(), "missing-controller", domain.SessionInterfaceTransitionDrain,
	); err != nil {
		t.Fatalf("prepare missing controller: %v", err)
	}
}

func TestServiceStopRetainsControllerUntilItsEventStreamActuallyEnds(t *testing.T) {
	base := newFakeConversation()
	h := newHarnessWithConversation(t, &stuckConversation{
		fakeConversation: base,
		closeErr:         errors.New("provider close failed"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := h.svc.Stop(ctx, testSession); err == nil {
		t.Fatal("Stop error = nil, want provider close failure or deadline")
	}
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("controller was forgotten while its stream was still live: %v", err)
	}
	if !h.svc.HasLiveChatController(testSession) {
		t.Fatal("live-controller guard cleared before the provider stream ended")
	}

	base.closeOnce.Do(func() { close(base.events) })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := h.svc.Controller(testSession); errors.Is(err, chatsvc.ErrNoController) {
			if h.svc.HasLiveChatController(testSession) {
				t.Fatal("live-controller guard remained set after registry release")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("controller registry did not release the stopped stream")
}

func TestServiceStopTerminatesPersistentConversation(t *testing.T) {
	t.Parallel()

	provider := &terminatingConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	if err := h.svc.Stop(context.Background(), testSession); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !provider.terminated.Load() {
		t.Fatal("explicit session stop detached persistent conversation instead of terminating it")
	}
}

func TestServiceStopAllRetainsControllerUntilItsEventStreamActuallyEnds(t *testing.T) {
	base := newFakeConversation()
	h := newHarnessWithConversation(t, &stuckConversation{
		fakeConversation: base,
		closeErr:         errors.New("provider close failed"),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	h.svc.StopAll(ctx)
	if _, err := h.svc.Controller(testSession); err != nil {
		t.Fatalf("controller was forgotten while its stream was still live: %v", err)
	}
	if !h.svc.HasLiveChatController(testSession) {
		t.Fatal("live-controller guard cleared before the provider stream ended")
	}

	base.closeOnce.Do(func() { close(base.events) })
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := h.svc.Controller(testSession); errors.Is(err, chatsvc.ErrNoController) {
			if h.svc.HasLiveChatController(testSession) {
				t.Fatal("live-controller guard remained set after registry release")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("controller registry did not release the stopped stream")
}

func TestServiceStopAllClosesHealthyControllerAfterStuckStreamExhaustsShutdownContext(t *testing.T) {
	st := openStore(t)
	now := time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)
	healthyRecord, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stuckSession := testSession
	healthySession := healthyRecord.ID
	if stuckSession >= healthySession {
		t.Fatalf("stuck session %s must sort before healthy session %s so StopAll hits the stuck stream first", stuckSession, healthySession)
	}

	stuckBase := newFakeConversation()
	stuckBase.providerConversationID = "stuck-thread"
	stuck := &stuckConversation{fakeConversation: stuckBase, closeErr: errors.New("provider close failed")}
	healthy := newFakeConversation()
	healthy.providerConversationID = "healthy-thread"
	var healthyClosed atomic.Bool
	healthy.onClose = func() { healthyClosed.Store(true) }

	var nextID atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				switch cfg.SessionID {
				case stuckSession:
					return stuck, nil
				case healthySession:
					return healthy, nil
				default:
					return nil, fmt.Errorf("unexpected session %s", cfg.SessionID)
				}
			},
		}},
		Log: slog.New(slog.DiscardHandler),
		NewID: func() string {
			return fmt.Sprintf("stopall-close-%d", nextID.Add(1))
		},
	})
	workspace := t.TempDir()
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: stuckSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("Start stuck: %v", err)
	}
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: healthySession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("Start healthy: %v", err)
	}
	t.Cleanup(func() {
		stuckBase.closeOnce.Do(func() { close(stuckBase.events) })
		_ = svc.Stop(context.Background(), stuckSession)
		_ = svc.Stop(context.Background(), healthySession)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	svc.StopAll(ctx)

	if !healthyClosed.Load() {
		t.Fatal("healthy controller was not closed after a stuck stream exhausted the shared shutdown context")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		_, err := svc.Controller(healthySession)
		if errors.Is(err, chatsvc.ErrNoController) {
			if svc.HasLiveChatController(healthySession) {
				t.Fatal("healthy live-controller guard remained set after detach")
			}
			if _, stuckErr := svc.Controller(stuckSession); stuckErr != nil {
				t.Fatalf("stuck controller was forgotten while its stream was still live: %v", stuckErr)
			}
			if !svc.HasLiveChatController(stuckSession) {
				t.Fatal("stuck live-controller guard cleared before the provider stream ended")
			}
			stuckBase.closeOnce.Do(func() { close(stuckBase.events) })
			releaseDeadline := time.Now().Add(time.Second)
			for time.Now().Before(releaseDeadline) {
				if _, err := svc.Controller(stuckSession); errors.Is(err, chatsvc.ErrNoController) {
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
			t.Fatal("stuck controller registry did not release the stopped stream")
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("healthy controller remained registered after StopAll initiated detach")
}

func TestServiceStopAllReturnsByDeadlineWhenControllerGateIsHeld(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 9, 14, 17, 0, 0, 0, time.UTC)
	healthyRecord, err := st.CreateSession(context.Background(), domain.SessionRecord{
		ProjectID: testProject, Kind: domain.KindOrchestrator, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	heldSession := testSession
	healthySession := healthyRecord.ID
	if heldSession >= healthySession {
		t.Fatalf("held session %s must sort before healthy session %s so StopAll waits on the contended gate first", heldSession, healthySession)
	}

	heldRelease := make(chan struct{})
	held := newFakeConversation()
	held.providerConversationID = "held-thread"
	held.closeStarted = make(chan struct{})
	held.closeEventsRelease = heldRelease
	healthy := newFakeConversation()
	healthy.providerConversationID = "healthy-thread"
	var healthyClosed atomic.Bool
	healthy.onClose = func() { healthyClosed.Store(true) }

	var nextID atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{
			start: func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
				switch cfg.SessionID {
				case heldSession:
					return held, nil
				case healthySession:
					return healthy, nil
				default:
					return nil, fmt.Errorf("unexpected session %s", cfg.SessionID)
				}
			},
		}},
		Log: slog.New(slog.DiscardHandler),
		NewID: func() string {
			return fmt.Sprintf("stopall-held-%d", nextID.Add(1))
		},
	})
	workspace := t.TempDir()
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: heldSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("Start held: %v", err)
	}
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: healthySession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("Start healthy: %v", err)
	}

	var releaseHeld sync.Once
	releaseHeldStream := func() { releaseHeld.Do(func() { close(heldRelease) }) }
	stopDone := make(chan error, 1)
	go func() { stopDone <- svc.Stop(context.Background(), heldSession) }()
	t.Cleanup(func() {
		releaseHeldStream()
		select {
		case <-stopDone:
		case <-time.After(time.Second):
		}
		_ = svc.Stop(context.Background(), healthySession)
	})
	select {
	case <-held.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Stop did not acquire the held session gate")
	}

	const shutdownTimeout = 40 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StopAll(ctx)
	}()
	select {
	case <-done:
	case <-time.After(shutdownTimeout + 200*time.Millisecond):
		t.Fatal("StopAll did not return by the shutdown deadline while a controller gate was held")
	}

	if !healthyClosed.Load() {
		t.Fatal("healthy controller was not closed while another session gate was held")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := svc.Controller(healthySession); errors.Is(err, chatsvc.ErrNoController) {
			if svc.HasLiveChatController(healthySession) {
				t.Fatal("healthy live-controller guard remained set after detach")
			}
			releaseHeldStream()
			select {
			case <-stopDone:
			case <-time.After(time.Second):
				t.Fatal("held Stop did not return after its stream was released")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("healthy controller remained registered after StopAll initiated detach")
}

func TestServiceStopAllOnlyDetachesPersistentConversation(t *testing.T) {
	t.Parallel()

	provider := &terminatingConversation{fakeConversation: newFakeConversation()}
	h := newHarnessWithConversation(t, provider)
	turn, err := h.ctrl.Send(context.Background(), ports.ChatUserMessage{Text: "keep working"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID,
		ProviderConversationID: provider.ProviderConversationID(),
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State == domain.TurnStateRunning
			}
		}
		return false
	})
	h.svc.StopAll(context.Background())
	if provider.terminated.Load() {
		t.Fatal("daemon-wide shutdown terminated persistent conversation")
	}
	rec, found, err := h.st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	if rec.Activity.State == domain.ActivityExited {
		t.Fatal("daemon detach projected a false provider exit")
	}
	snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range snapshot.Turns {
		if candidate.ID == turn.ID && candidate.State != domain.TurnStateRunning {
			t.Fatalf("in-flight turn settled on daemon detach: %s", candidate.State)
		}
	}
}

func TestServiceLiveReconnectSkipsSettledHistoryBarrier(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	native := &nativeHistoryConversation{fakeConversation: newFakeConversation(), err: ports.ErrChatHistoryUnsettled}
	provider := &liveReconnectedConversation{nativeHistoryConversation: native}
	var prepareCalls atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Reader: fullSnapshotReader(st), Sessions: st, Drivers: fakeRegistry{driver: fakeDriver{
			conv:  provider,
			probe: func() error { return ports.ErrChatDriverUnavailable },
		}},
		Log: slog.New(slog.DiscardHandler), NewID: func() string { return "live-reconnect-id" },
	})
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
		PrepareControllerEnv: func(context.Context, domain.SessionControllerOwner, domain.AgentHarness) (map[string]string, error) {
			prepareCalls.Add(1)
			return map[string]string{"TALLPINE_BROWSER_CAPABILITY": "rotated"}, nil
		},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer svc.StopAll(context.Background())
	if reads := native.historyReads(); reads != 0 {
		t.Fatalf("native history reads = %d, live reconnect must not wait for active turn to settle", reads)
	}
	if got := prepareCalls.Load(); got != 0 {
		t.Fatalf("live reconnect rotated launch-only credentials %d times, want 0", got)
	}
}

func TestServiceLiveReconnectKeepsDurableRunningTurnBusy(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	reader := fullSnapshotReader(st)
	var ids atomic.Int32
	newID := func() string { return fmt.Sprintf("reconnect-%d", ids.Add(1)) }
	firstProvider := &terminatingConversation{fakeConversation: newFakeConversation()}
	first := chatsvc.New(chatsvc.Options{
		Store: st, Reader: reader, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: firstProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	firstController, err := first.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	turn, err := firstController.Send(context.Background(), ports.ChatUserMessage{Text: "keep working"})
	if err != nil {
		t.Fatalf("first Send: %v", err)
	}
	firstProvider.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID,
		ProviderConversationID: firstProvider.ProviderConversationID(),
	})
	h := &harness{st: st, ctrl: firstController}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ID == turn.ID {
				return candidate.State == domain.TurnStateRunning
			}
		}
		return false
	})
	first.StopAll(context.Background())

	before, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("read before restart: found=%v err=%v", found, err)
	}
	before.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Unix(100, 0).UTC()}
	before.Metadata.ProviderConversationID = firstProvider.ProviderConversationID()
	if err := st.UpdateSession(context.Background(), before); err != nil {
		t.Fatal(err)
	}
	lcm := lifecycle.New(st, nil)
	secondProvider := &liveReconnectedConversation{nativeHistoryConversation: &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
	}}
	second := chatsvc.New(chatsvc.Options{
		Store: st, Reader: reader, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: secondProvider}},
		Log:     slog.New(slog.DiscardHandler), NewID: newID,
	})
	t.Cleanup(func() { second.StopAll(context.Background()) })
	secondController, err := second.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: firstProvider.ProviderConversationID(),
		ControllerReady: func(result chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			if !result.LiveReconnect {
				t.Fatal("same live provider was reported as a fresh spawn")
			}
			return chatsvc.ControllerCommit{}, lcm.MarkChatReconnected(context.Background(), testSession, domain.SessionMetadata{
				ProviderConversationID: result.ProviderConversationID, ControllerGeneration: result.ControllerGeneration,
			})
		},
	})
	if err != nil {
		t.Fatalf("reconnect Start: %v", err)
	}
	after, _, err := st.GetSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	if after.Activity != before.Activity || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("reconnect changed activity/recency: before=%+v after=%+v", before, after)
	}
	if after.Metadata.ControllerGeneration == before.Metadata.ControllerGeneration {
		t.Fatal("generation did not rotate")
	}
	// ACP adapters emit their initialized ready state before live reconnect
	// restores ownership of the durable turn. The queued notification is stale
	// once that ownership has been restored and must not make the controller
	// claim it is ready while the durable turn is still running.
	secondProvider.emit(ports.ChatEvent{
		Kind: ports.ChatEventControllerState, ProviderEventID: "reconnect-ready",
		ControllerState: ports.ChatControllerReady,
	})
	deadline := time.Now().Add(time.Second)
	readyProjected := false
	for time.Now().Before(deadline) {
		events, readErr := st.ProviderEventsSince(context.Background(), secondController.ConversationID(), 0, 10_000)
		if readErr != nil {
			t.Fatal(readErr)
		}
		seen := false
		for _, event := range events {
			if event.ProviderEventID == "reconnect-ready" {
				seen = true
				break
			}
		}
		if seen {
			readyProjected = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !readyProjected {
		t.Fatal("reconnect ready event was not projected")
	}
	if got := secondController.State(); got != ports.ChatControllerBusy {
		t.Fatalf("controller state after reconnect ready event = %q, want busy for durable running turn", got)
	}
	queued, err := secondController.Send(context.Background(), ports.ChatUserMessage{Text: "after restart"})
	if err != nil {
		t.Fatalf("reconnect Send: %v", err)
	}
	if queued.State != domain.TurnStateQueued {
		t.Fatalf("reconnect Send state = %s, want queued behind surviving turn", queued.State)
	}
	if got := secondProvider.sentTexts(); len(got) != 0 {
		t.Fatalf("reconnected provider received concurrent turns: %v", got)
	}
}

func TestStartWaitsForStoppedControllerCleanupBeforeRelaunch(t *testing.T) {
	st := openStore(t)
	first := newFakeConversation()
	second := newFakeConversation()
	driver := &sequenceDriver{conversations: []ports.ChatConversation{first, second}}
	var idMu sync.Mutex
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: driver},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("relaunch-id-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	firstController, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first Start: %v", err)
	}
	first.emit(ports.ChatEvent{
		Kind:            ports.ChatEventControllerState,
		ControllerState: ports.ChatControllerStopped,
	})
	deadline := time.Now().Add(time.Second)
	for firstController.State() != ports.ChatControllerStopped && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := firstController.State(); got != ports.ChatControllerStopped {
		t.Fatalf("first controller state = %q, want stopped", got)
	}
	if svc.HasLiveChatController(testSession) {
		t.Fatal("stopped controller reported live")
	}

	replacementWorkspace := t.TempDir()
	waitCtx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	controller, err := svc.Start(waitCtx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: replacementWorkspace, ProviderConversationID: "thread-1",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Start while stopped controller was cleaning up = controller=%p err=%v, want deadline", controller, err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("close first conversation: %v", err)
	}
	firstController.Wait()
	replacement, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: replacementWorkspace, ProviderConversationID: "thread-1",
	})
	if err != nil {
		t.Fatalf("relaunch: %v", err)
	}
	if replacement == firstController {
		t.Fatal("relaunch returned the stopped controller")
	}
}

func TestConcurrentReconcileAndResumeShareOneCredentialedControllerLaunch(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conversation := newFakeConversation()
	providerStarted := make(chan struct{})
	releaseProvider := make(chan struct{})
	var providerOnce sync.Once
	var providerStarts atomic.Int32
	var launchedEnv map[string]string
	driver := fakeDriver{conv: conversation}
	driver.start = func(cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
		providerStarts.Add(1)
		launchedEnv = maps.Clone(cfg.Env)
		providerOnce.Do(func() { close(providerStarted) })
		<-releaseProvider
		return conversation, nil
	}

	var prepared atomic.Int32
	var ids atomic.Int32
	prepare := func(context.Context, domain.SessionControllerOwner, domain.AgentHarness) (map[string]string, error) {
		call := prepared.Add(1)
		return map[string]string{"TALLPINE_BROWSER_CAPABILITY": fmt.Sprintf("token-%d", call)}, nil
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st, Drivers: fakeRegistry{driver: driver},
		Log: slog.New(slog.DiscardHandler), NewID: func() string { return fmt.Sprintf("gate-id-%d", ids.Add(1)) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	cfg := chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), Env: map[string]string{"TALLPINE_BROWSER_CAPABILITY": "stale"},
		PrepareControllerEnv: prepare,
	}
	type startResult struct {
		controller *chatsvc.Controller
		err        error
	}
	results := make(chan startResult, 2)
	go func() {
		controller, err := svc.Start(context.Background(), cfg)
		results <- startResult{controller, err}
	}()
	<-providerStarted
	secondEntered := make(chan struct{})
	go func() {
		close(secondEntered)
		controller, err := svc.Start(context.Background(), cfg)
		results <- startResult{controller, err}
	}()
	<-secondEntered
	select {
	case result := <-results:
		t.Fatalf("a concurrent Start escaped the controller gate: controller=%p err=%v", result.controller, result.err)
	case <-time.After(25 * time.Millisecond):
	}
	if got := prepared.Load(); got != 1 {
		t.Fatalf("credential preparations while first launch is blocked = %d, want 1", got)
	}
	close(releaseProvider)
	first := <-results
	second := <-results
	if first.err != nil || second.err != nil || first.controller != second.controller {
		t.Fatalf("concurrent Starts = (%p, %v), (%p, %v), want the same live controller",
			first.controller, first.err, second.controller, second.err)
	}
	if got := providerStarts.Load(); got != 1 {
		t.Fatalf("provider starts = %d, want 1", got)
	}
	if got := launchedEnv["TALLPINE_BROWSER_CAPABILITY"]; got != "token-1" {
		t.Fatalf("provider capability = %q, want token-1", got)
	}
}

// The cancellation belongs to the moment stop was pressed. A message typed after
// that is the user asking for new work, and must not be swept up by it.
func TestMessageTypedAfterStopIsStillDelivered(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "before stop", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("Send before stop: %v", err)
	}

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	// The user changes their mind and types again while the interrupt lands.
	h.advance(time.Second)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "after stop", ClientMessageID: "c3",
	}); err != nil {
		t.Fatalf("Send after stop: %v", err)
	}
	h.conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateInterrupted,
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["after stop"] == domain.TurnStateRunning
	})
	states := turnStateByText(t, snapshot)
	if states["before stop"] != domain.TurnStateInterrupted {
		t.Errorf("pre-stop message = %q, want interrupted", states["before stop"])
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "after stop" {
		t.Fatalf("provider received %v, want the post-stop message delivered", got)
	}
}

// The initial prompt is the user's task brief, so it must render as their message
// rather than as a system notice. Origin records who authored a message, not who
// delivered it to the provider.
func TestInitialPromptIsAttributedToTheUser(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.StartChatTurn(ctx, testSession, "Explain the whole design system"); err != nil {
		t.Fatalf("StartChatTurn: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	first := snapshot.Messages[0]
	if first.Origin != domain.MessageOriginHuman {
		t.Fatalf("initial prompt origin = %q, want %q — the daemon delivers it but the user wrote it",
			first.Origin, domain.MessageOriginHuman)
	}
	if first.Role != domain.MessageRoleUser {
		t.Errorf("initial prompt role = %q, want user", first.Role)
	}
}

// A relayed message is Tallpine carrying someone else's words: `tallpine send`, or an
// orchestrator writing to a worker. It must be attributed to automation, not
// passed off as something the user typed here — the timeline distinguishes the
// two structurally, and a reader should never have to infer it from a prefix.
func TestRelayedMessageIsAttributedToAutomation(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.RelayChatTurn(ctx, testSession, "orchestrator: rebase onto main"); err != nil {
		t.Fatalf("RelayChatTurn: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	msg := snapshot.Messages[0]
	if msg.Origin != domain.MessageOriginAutomation {
		t.Fatalf("relay origin = %q, want %q", msg.Origin, domain.MessageOriginAutomation)
	}
	if msg.Role != domain.MessageRoleUser {
		t.Errorf("relay role = %q; a relay is still an inbound request", msg.Role)
	}
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != "orchestrator: rebase onto main" {
		t.Fatalf("provider received %v, want the relayed text dispatched", got)
	}
	rec, ok, err := h.st.GetSession(ctx, testSession)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if !rec.Metadata.LatestUserPromptAt.IsZero() {
		t.Fatalf("automation advanced latest user prompt time to %s", rec.Metadata.LatestUserPromptAt)
	}
}

func TestUserAuthoredRelayAdvancesUserActivityWithoutChangingOrigin(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.RelayUserAuthoredChatTurn(ctx, testSession, "move this control closer to the heading"); err != nil {
		t.Fatalf("RelayUserAuthoredChatTurn: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Messages) >= 1
	})
	if got := snapshot.Messages[0].Origin; got != domain.MessageOriginAutomation {
		t.Fatalf("user-authored relay origin = %q, want %q", got, domain.MessageOriginAutomation)
	}
	rec, ok, err := h.st.GetSession(ctx, testSession)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if rec.Metadata.LatestUserPrompt != "move this control closer to the heading" || !rec.Metadata.LatestUserPromptAt.Equal(h.now()) {
		t.Fatalf("latest user prompt = %q at %s, want annotation at %s",
			rec.Metadata.LatestUserPrompt, rec.Metadata.LatestUserPromptAt, h.now())
	}
}

// Stop appears the moment a message is sent, so a user can press it before the
// provider has acknowledged the turn — and a provider refuses to cancel a turn it
// does not yet consider active. Interrupt waits out that gap rather than handing
// back a failure in the exact moment someone realizes they sent the wrong thing.
func TestInterruptWaitsForTheProviderToAcknowledgeTheTurn(t *testing.T) {
	t.Parallel()

	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// The acknowledgement lands while Interrupt is already waiting.
	go func() {
		time.Sleep(150 * time.Millisecond)
		conv.markActive("provider-turn-1")
		conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	}()

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt raced the provider's acknowledgement: %v", err)
	}
	if got := conv.attemptCount(); got != 1 {
		t.Errorf("interrupt attempts = %d, want 1", got)
	}
}

// Interrupt can begin while Send still owns the dispatch lock. Once dispatch
// finishes, Stop must still wait for turn-started rather than treating the
// provider's early refusal as proof that the new turn is stale.
func TestInterruptWaitsForAcknowledgementAfterRacingDispatch(t *testing.T) {
	t.Parallel()

	conv := newBlockingDispatchConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	sendDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: "go", ClientMessageID: "dispatch-race",
		})
		sendDone <- err
	}()
	select {
	case <-conv.sendStarted:
	case <-time.After(4 * time.Second):
		t.Fatal("Send did not enter provider dispatch")
	}

	interruptDone := make(chan error, 1)
	go func() { interruptDone <- h.svc.Interrupt(ctx, testSession) }()
	close(conv.releaseSend)
	if err := <-sendDone; err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case <-conv.attempted:
		t.Fatal("Interrupt reached the provider before turn-started acknowledgement")
	case <-time.After(100 * time.Millisecond):
	}
	conv.markActive("provider-turn-1")
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	if err := <-interruptDone; err != nil {
		t.Fatalf("Interrupt after acknowledgement: %v", err)
	}
}

// A provider that refuses because the turn is genuinely gone must not strand the
// user: the durable row still says running — that is what the UI renders — so
// Interrupt reconciles it as interrupted instead of answering "nothing to stop"
// while the Working bar stays up.
func TestProviderRefusalReconcilesTheDurableTurnAsInterrupted(t *testing.T) {
	t.Parallel()

	conv := newInterruptRecorder() // never marks anything active
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "go", ClientMessageID: "c1"}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateInterrupted
	})
	if got := snapshot.Turns[0].ErrorMessage; got != "" {
		t.Errorf("interrupted turn error message = %q, want empty", got)
	}
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityIdle, "chat.interrupt.reconciled") {
		t.Errorf("no idle signal after reconciliation; signals = %v", h.activity.snapshot())
	}
}

// The recovery path must keep the same cutoff as the Stop request. The composer
// remains available while provider cancellation is pending, and a later prompt
// is new user intent rather than part of the queue that Stop was asked to clear.
func TestProviderRefusalPreservesMessageQueuedAfterStop(t *testing.T) {
	t.Parallel()

	conv := newBlockingInterruptRefusalConversation()
	t.Cleanup(conv.unblock)
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send running: %v", err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "before stop", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("Send before stop: %v", err)
	}

	interruptDone := make(chan error, 1)
	go func() { interruptDone <- h.svc.Interrupt(ctx, testSession) }()
	select {
	case <-conv.started:
	case <-time.After(4 * time.Second):
		t.Fatal("provider interrupt did not start")
	}

	h.advance(time.Second)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "after stop", ClientMessageID: "c3",
	}); err != nil {
		t.Fatalf("Send after stop: %v", err)
	}
	conv.unblock()
	if err := <-interruptDone; err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["before stop"] == domain.TurnStateInterrupted &&
			states["after stop"] != domain.TurnStateQueued
	})
	states := turnStateByText(t, snapshot)
	if got := states["after stop"]; got != domain.TurnStateRunning {
		t.Fatalf("post-stop message = %q, want running", got)
	}
	if got := conv.sentTexts(); len(got) != 2 || got[1] != "after stop" {
		t.Fatalf("provider received %v, want the post-stop message delivered", got)
	}
}

// A provider can publish completion just before its interrupt call reports that
// no active turn remains. If that completion commits first, reconciliation must
// not overwrite it or the queue transition it already performed.
func TestProviderRefusalDoesNotOverwriteCommittedCompletion(t *testing.T) {
	t.Parallel()

	conv := newCompletionBeforeRefusalConversation()
	t.Cleanup(conv.unblock)
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send running: %v", err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "before stop", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("Send before stop: %v", err)
	}

	interruptDone := make(chan error, 1)
	go func() { interruptDone <- h.svc.Interrupt(ctx, testSession) }()
	select {
	case <-conv.emitted:
	case <-time.After(4 * time.Second):
		t.Fatal("provider completion was not emitted")
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["running"] == domain.TurnStateCompleted &&
			states["before stop"] == domain.TurnStateInterrupted
	})

	conv.unblock()
	if err := <-interruptDone; err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["running"].Terminal()
	})
	if got := turnStateByText(t, snapshot)["running"]; got != domain.TurnStateCompleted {
		t.Fatalf("turn after committed completion = %q, want completed", got)
	}
}

// The #3749 desync: a turn is 'running' on disk while the in-memory controller
// has no pendingTurnID (a completed projection failed, or the daemon restarted
// mid-turn). The UI reads disk and shows "Working"; Stop must act on what the
// user sees rather than refuse with CHAT_NO_ACTIVE_TURN.
func TestInterruptReconcilesStaleRunningTurnOnDisk(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()

	// Create the running turn directly in the store, bypassing the controller so
	// its pendingTurnID stays empty — the desynced state.
	const providerTurnID = "stale-running-turn"
	if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession,
		h.ctrl.Generation(), "stale-turn-id", providerTurnID, h.now()); err != nil {
		t.Fatalf("AdoptProviderTurn: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateInterrupted
	})
	if !hasActivitySignal(h.activity.snapshot(), domain.ActivityIdle, "chat.interrupt.reconciled") {
		t.Errorf("no idle signal after reconciliation; signals = %v", h.activity.snapshot())
	}
}

// A root and a nested provider turn can both be durably running. The UI renders
// the oldest visible row first, but clearing only that row would merely reveal a
// second Working bar. Recovery cancels the visible root and settles the full
// visible running set.
func TestInterruptReconcilesAllVisibleRunningTurns(t *testing.T) {
	t.Parallel()

	conv := newInterruptRecorder()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession,
		h.ctrl.Generation(), "root-turn", "provider-root", h.now()); err != nil {
		t.Fatalf("AdoptProviderTurn root: %v", err)
	}
	h.advance(time.Second)
	if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession,
		h.ctrl.Generation(), "child-turn", "provider-child", h.now()); err != nil {
		t.Fatalf("AdoptProviderTurn child: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[0].State == domain.TurnStateRunning &&
			s.Turns[1].State == domain.TurnStateRunning
	})

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[0].State.Terminal() && s.Turns[1].State.Terminal()
	})
	for _, turn := range snapshot.Turns {
		if turn.State != domain.TurnStateInterrupted {
			t.Errorf("turn %s = %q, want interrupted", turn.ProviderTurnID, turn.State)
		}
	}
	conv.activeMu.Lock()
	attempts := append([]string(nil), conv.attempts...)
	conv.activeMu.Unlock()
	if len(attempts) != 1 || attempts[0] != "provider-root" {
		t.Fatalf("provider interrupt attempts = %v, want visible root first", attempts)
	}
}

// In the no-memory recovery path, provider cancellation happens under sendMu.
// A prompt submitted after Stop therefore waits for stale settlement and then
// starts as new work instead of replacing the recovery target.
func TestInterruptDurableFallbackPreservesPostStopSend(t *testing.T) {
	t.Parallel()

	conv := newBlockingInterruptRefusalConversation()
	t.Cleanup(conv.unblock)
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if err := h.st.AdoptProviderTurn(ctx, h.ctrl.ConversationID(), testSession,
		h.ctrl.Generation(), "stale-turn", "provider-stale", h.now()); err != nil {
		t.Fatalf("AdoptProviderTurn: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})

	interruptDone := make(chan error, 1)
	go func() { interruptDone <- h.svc.Interrupt(ctx, testSession) }()
	select {
	case <-conv.started:
	case <-time.After(4 * time.Second):
		t.Fatal("durable fallback did not reach provider interrupt")
	}

	h.advance(time.Second)
	sendDone := make(chan error, 1)
	go func() {
		_, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
			Text: "after stop", ClientMessageID: "post-stop-fallback",
		})
		sendDone <- err
	}()
	select {
	case err := <-sendDone:
		t.Fatalf("post-Stop Send crossed reconciliation lock early: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	conv.unblock()
	if err := <-interruptDone; err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	if err := <-sendDone; err != nil {
		t.Fatalf("post-Stop Send: %v", err)
	}
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 2 && s.Turns[0].State == domain.TurnStateInterrupted &&
			s.Turns[1].State == domain.TurnStateRunning
	})
	if snapshot.Turns[0].ProviderTurnID != "provider-stale" {
		t.Fatalf("reconciled turn = %q, want provider-stale", snapshot.Turns[0].ProviderTurnID)
	}
	if got := conv.sentTexts(); len(got) != 1 || got[0] != "after stop" {
		t.Fatalf("provider received %v, want only post-Stop work", got)
	}
}

// When memory and disk agree there is nothing running, Interrupt must still
// answer ErrNoActiveTurn — the disk fallback must not invent work to stop.
func TestInterruptReturnsNoActiveTurnWhenNoRunningTurnAnywhere(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	err := h.svc.Interrupt(context.Background(), testSession)
	if !errorsIs(err, chatsvc.ErrNoActiveTurn) {
		t.Fatalf("err = %v, want ErrNoActiveTurn", err)
	}
}

// Reconciliation is still the user's brake: anything queued behind the stale
// turn is cancelled, not released into the provider.
func TestInterruptReconciliationCancelsQueuedTurns(t *testing.T) {
	t.Parallel()

	conv := newInterruptRecorder() // never marks anything active
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "running", ClientMessageID: "c1",
	}); err != nil {
		t.Fatalf("Send running: %v", err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "queued", ClientMessageID: "c2",
	}); err != nil {
		t.Fatalf("Send queued: %v", err)
	}

	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		states := turnStateByText(t, s)
		return states["running"] == domain.TurnStateInterrupted &&
			states["queued"] == domain.TurnStateInterrupted
	})
	states := turnStateByText(t, snapshot)
	if states["queued"] != domain.TurnStateInterrupted {
		t.Errorf("queued turn = %q, want interrupted (cancelled, not dispatched)", states["queued"])
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; reconciliation must not release the queue", got)
	}
}

// A daemon that is killed never runs its own cleanup, so whatever the dead
// controller left in flight is still marked live on disk. The next controller to
// come up has to close it out: nothing else ever will, and until then the timeline
// claims a turn is running behind a controller that no longer exists. A message
// queued behind that turn never reached the provider, so it stays queued and is
// delivered exactly once by the next controller, even across another restart.
func TestStartSettlesWorkLeftByAKilledController(t *testing.T) {
	t.Parallel()

	testStartSettlesWorkLeftByAKilledController(t, false)
}

func TestStartSettlesQueuedWorkLeftByAKilledController(t *testing.T) {
	t.Parallel()

	testStartSettlesWorkLeftByAKilledController(t, true)
}
