package chat_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// B520: a turn the provider refused for authentication leaves the session
// reporting a required sign-in, not only waiting_input.
func TestServiceReportsReauthRequiredAfterAuthRefusal(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "first", ClientMessageID: "c1", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("reported a reauth before any refusal")
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed,
		Err: fmt.Errorf("%w: ACP session/prompt: Authentication required", ports.ErrChatAuthRequired)})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	if !h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("ReauthRequired = false after the provider refused the login")
	}
	if h.svc.ReauthRequired(ctx, "no-such-session") {
		t.Fatal("a session without a controller must report no reauth")
	}
}

func (f *fakeConversation) sentCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

// B524: a send after a refused login is unconfirmed, not undelivered. The turn
// still reaches the provider, so a login that was renewed runs the message and
// a resend would deliver it twice. The reauth flag then tells the two outcomes
// apart: a refused attempt leaves it set, a completed one clears it.
func TestRelayToReauthRequiredSessionIsUnconfirmedUntilTheTurnEnds(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	baseline, err := newHarness(t).svc.RelaySessionChatTurnReceipt(ctx, testSession, "first", "c0", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Outcome != ports.MessageDelivered {
		t.Fatalf("send with no refusal = %+v, want delivered", baseline)
	}

	h := newHarness(t)
	refusal := fmt.Errorf("%w: ACP session/prompt: Authentication required", ports.ErrChatAuthRequired)
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "second", ClientMessageID: "c2", Origin: domain.MessageOriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID, TurnState: domain.TurnStateFailed, Err: refusal})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	unconfirmedRelay := func(text, id string) string {
		t.Helper()
		sentBefore := h.conv.sentCount()
		receipt, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession, text, id, ports.MessageDeliveryOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if receipt.Outcome != ports.MessageReauthRequired || receipt.ID == "" {
			t.Fatalf("send after the refusal = %+v, want reauth_required with the attempted turn id", receipt)
		}
		if got := h.conv.sentCount(); got != sentBefore+1 {
			t.Fatalf("provider sends = %d, want %d: the attempt must reach the provider", got, sentBefore+1)
		}
		var providerTurn string
		h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
			for _, candidate := range s.Turns {
				if candidate.ID == receipt.ID {
					providerTurn = candidate.ProviderTurnID
				}
			}
			return providerTurn != ""
		})
		return providerTurn
	}

	// Login still dead: the attempt fails and the flag says to sign in and resend.
	failed := unconfirmedRelay("third", "c3")
	h.advance(time.Second)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: failed, TurnState: domain.TurnStateFailed, Err: refusal})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, candidate := range s.Turns {
			if candidate.ProviderTurnID == failed {
				return candidate.State == domain.TurnStateFailed
			}
		}
		return false
	})
	if !h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("a refused attempt must leave the sign-in demand set")
	}

	// Login renewed: the same status, but the message ran. The flag clearing is
	// the sender's signal not to resend.
	h.advance(time.Second)
	ran := unconfirmedRelay("fourth", "c4")
	h.advance(time.Second)
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: ran},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: ran, TurnState: domain.TurnStateCompleted},
	)
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt == nil
	})
	if h.svc.ReauthRequired(ctx, testSession) {
		t.Fatal("a completed attempt must clear the sign-in demand")
	}
	after, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession, "fifth", "c5", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if after.Outcome != ports.MessageDelivered {
		t.Fatalf("send after recovery = %+v, want delivered", after)
	}
}
