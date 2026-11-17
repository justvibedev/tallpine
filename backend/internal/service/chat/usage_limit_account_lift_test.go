package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// siblingCompletesTurn settles a completed turn at when on a second session
// bound to account, as another agent on that account would.
func siblingCompletesTurn(t *testing.T, st *sqlite.Store, id domain.SessionID, account string, when time.Time) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.CreateSession(ctx, domain.SessionRecord{
		ID: id, ProjectID: testProject, Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Mode: domain.SessionModeChat, CreatedAt: when, UpdatedAt: when,
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.BindSessionAgentAccount(ctx, id, domain.AgentAccountProviderCodex, account, when.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	conv, err := st.CreateConversation(ctx, "conv-"+string(id), domain.ConversationScopeSession, testProject, id, when)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendUserMessage(ctx, conv.ID, id, "gen", domain.ConversationMessage{
		ID: "msg-" + string(id), Origin: domain.MessageOriginHuman, Text: "work", ClientMessageID: "cm-" + string(id),
	}, "turn-"+string(id), when); err != nil {
		t.Fatal(err)
	}
	if err := st.SettleTurnByID(ctx, "turn-"+string(id), domain.TurnStateCompleted, "", when); err != nil {
		t.Fatal(err)
	}
}

// A session paused on the provider's reset time resumes as soon as another
// session on the same account completes a turn, and its queued automation drains.
// The send that was held says so.
func TestUsageLimitPauseLiftsWhenAccountCompletesTurn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})

	receipt, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession, "nudge", "", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != ports.MessageQueued || receipt.HeldUntil.Year() != 2099 {
		t.Fatalf("receipt = %+v, want queued and held until the provider reset", receipt)
	}

	// Another account completing a turn proves nothing about this one.
	siblingCompletesTurn(t, h.st, "p1-2", "account-b", h.now().Add(time.Minute))
	h.svc.WakeUsageLimitPaused("p1-2", domain.HarnessCodex)
	time.Sleep(300 * time.Millisecond)
	if got := h.conv.sentTexts(); len(got) != 1 {
		t.Fatalf("provider received %v; a turn on another account must not lift the pause", got)
	}

	siblingCompletesTurn(t, h.st, "p1-3", "account-a", h.now().Add(2*time.Minute))
	h.svc.WakeUsageLimitPaused("p1-3", domain.HarnessCodex)
	deadline := time.Now().Add(10 * time.Second)
	for len(h.conv.sentTexts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := h.conv.sentTexts(); len(got) != 2 || got[1] != "nudge" {
		t.Fatalf("provider received %v, want the held nudge once the account completed a turn", got)
	}
	if _, paused := h.svc.UsageLimitPausedUntil(ctx, testSession); paused {
		t.Fatal("still paused after the account completed a turn")
	}
}

// A daemon restart loses the in-memory pause wake. The restarted controller
// re-arms it from the durable failed turn, so a same-account turn still lifts
// the pause and drains the queue.
func TestUsageLimitPauseLiftsAfterDaemonRestart(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge", ClientMessageID: "n1", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}

	provider := newSteerRecorder()
	provider.turnSeq = 100
	restarted := crashRestart(t, h, provider)
	time.Sleep(200 * time.Millisecond)
	if got := provider.sentTexts(); len(got) != 0 {
		t.Fatalf("restarted provider received %v while still paused", got)
	}

	siblingCompletesTurn(t, h.st, "p1-2", "account-a", h.now().Add(time.Minute))
	restarted.WakeUsageLimitPaused("p1-2", domain.HarnessCodex)
	deadline := time.Now().Add(10 * time.Second)
	for len(provider.sentTexts()) < 1 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := provider.sentTexts(); len(got) != 1 {
		t.Fatalf("restarted provider received %v, want the held nudge after the account completed a turn", got)
	}
}

// A controller that was paused once, then failed an ordinary turn, holds its
// queue (issue #4861). A sibling's completed turn must not drain it.
func TestSiblingTurnDoesNotDrainControllerAfterOrdinaryFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.BindSessionAgentAccount(ctx, testSession, domain.AgentAccountProviderCodex, "account-a", h.now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("You've hit your 5-hour limit · resets 2099-01-01T00:00:00Z")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["first"] == domain.TurnStateFailed
	})
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "nudge", ClientMessageID: "n1", Origin: domain.MessageOriginAutomation}); err != nil {
		t.Fatal(err)
	}
	// The human's own message goes through and fails for an ordinary reason.
	second, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "mine", ClientMessageID: "c2", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: second.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: errors.New("connection reset")})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return turnStateByText(t, s)["mine"] == domain.TurnStateFailed
	})
	sent := len(h.conv.sentTexts())

	siblingCompletesTurn(t, h.st, "p1-2", "account-a", h.now().Add(time.Minute))
	h.svc.WakeUsageLimitPaused("p1-2", domain.HarnessCodex)
	time.Sleep(500 * time.Millisecond)
	if got := h.conv.sentTexts(); len(got) != sent {
		t.Fatalf("provider received %v; a sibling's turn must not drain a queue held by an ordinary failure", got)
	}
}
