package chat_test

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// B491: a steer aborts the running tool call, so Tallpine's own notices (no
// sender, not user-authored) must queue behind the turn instead.
func TestAutomatedNoticeToBusyTurnQueuesWithoutSteering(t *testing.T) {
	t.Parallel()
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"Tallpine review of PR #1 requested changes", "", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != ports.MessageQueued {
		t.Fatalf("receipt = %+v, want queued", receipt)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("automated notice steered (would cancel the running tool): %+v", calls)
	}
	// The queued notice is delivered once the running turn ends.
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted,
		ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(provider.sentTexts()) == 2 })
	if got := provider.sentTexts(); !strings.Contains(got[1], "requested changes") {
		t.Fatalf("provider turns = %v, want the notice delivered after the turn", got)
	}
}

func TestOrchestratorMessageToBusyTurnStillSteers(t *testing.T) {
	t.Parallel()
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"hold, don't push", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != ports.MessageDelivered || len(provider.steers()) != 1 {
		t.Fatalf("receipt = %+v steers = %+v, want one steer", receipt, provider.steers())
	}
}
