package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// A nudge queued while the failing turn ran, and one sent after it, both stay
// queued once the turn fails on a provider quota; a human message still goes
// through on its own and leaves the nudges queued.
func TestAutomationSendsStayQueuedWhileUsageLimitPaused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()

	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: turn.ProviderTurnID})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
	})
	// (a) queued while the failing turn is still running.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge1", ClientMessageID: "n1", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed,
		Err:       errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v after the failure; queued nudge must wait", got)
	}
	// (b) a second automation send after the first one queued.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge2", ClientMessageID: "n2", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; the second nudge must wait", got)
	}
	// A human message goes through alone, ahead of the queued nudges.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "mine", ClientMessageID: "c2", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "mine" {
		t.Fatalf("provider received %v, want exactly the human message second", got)
	}
	states := turnStateByText(t, h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return true }))
	if states["nudge1"] != domain.TurnStateQueued || states["nudge2"] != domain.TurnStateQueued {
		t.Fatalf("turn states = %v, want both nudges still queued", states)
	}
}

func failWithNearReset(t *testing.T, h *harness) {
	t.Helper()
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	reset := h.now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New("You've hit your 5-hour limit · resets " + reset)})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge", ClientMessageID: "n1", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
}

func TestQueuedNudgeDispatchesAfterUsageLimitReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	failWithNearReset(t, h)
	h.advance(time.Minute)
	deadline := time.Now().Add(10 * time.Second)
	for len(h.conv.sentTexts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "nudge" {
		t.Fatalf("provider received %v, want the queued nudge after the reset", got)
	}
}

func TestServiceReportsUsageLimitPausedUntil(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if _, paused := h.svc.UsageLimitPausedUntil(ctx, testSession); paused {
		t.Fatal("reported a pause before any failure")
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	until, paused := h.svc.UsageLimitPausedUntil(ctx, testSession)
	if !paused || until.Year() != 2099 {
		t.Fatalf("pausedUntil = %v, %v, want the provider reset", until, paused)
	}
}
