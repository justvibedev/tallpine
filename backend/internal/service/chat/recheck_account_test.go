package chat_test

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func TestATurnAfterARefusedLoginReadsTheAccountAgain(t *testing.T) {
	h := newHarness(t)
	countsAfter := func(changes, rechecks int32) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for h.accountChecks.Load() != changes || h.accountRechecks.Load() != rechecks {
			if time.Now().After(deadline) {
				t.Fatalf("account changes, rechecks = %d, %d; want %d, %d",
					h.accountChecks.Load(), h.accountRechecks.Load(), changes, rechecks)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "first"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 1 })
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "first", TurnState: domain.TurnStateFailed, Err: ports.ErrChatAuthRequired})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Account != nil && s.Conversation.Account.ReauthRequiredAt != nil
	})
	countsAfter(1, 0) // the refusal is an account change; the first turn had no demand
	// The user signed in again, maybe with the CLI outside AO, and sent a message.
	// That is a recheck, not another refusal: it must not invalidate readiness.
	h.advance(time.Second)
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "second"})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(s.Turns) == 2 })
	countsAfter(1, 1)
}
