package chat_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// A worker that asked an in-chat question (AskUserQuestion) is blocked inside
// its running turn. A relayed `tallpine send` that queued behind that turn would never
// be delivered, while the sender believed it had answered.

type inputAsker interface {
	emit(events ...ports.ChatEvent)
}

// openQuestion starts a turn and has the provider ask a structured question in
// it, then waits until the controller knows the turn is blocked on it.
func openQuestion(t *testing.T, h *harness, provider inputAsker) {
	t.Helper()
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(
		ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"},
		ports.ChatEvent{
			Kind: ports.ChatEventInputRequested, ProviderTurnID: "provider-turn-1",
			ProviderItemID: "question-1", RequestID: "question-request-1",
			Input: &ports.ChatInputRequest{
				Mode: ports.ChatInputModeForm, Message: "Which option?",
				Schema: map[string]any{"type": "object"},
			},
		},
	)
	awaitWaitingOnInput(t, h, true)
}

// forgetfulOnStop answers Stop with "no active turn", which makes the
// controller settle the turn without a provider completion.
type forgetfulOnStop struct{ *steerRecorder }

func (f *forgetfulOnStop) Interrupt(context.Context, string) error { return ports.ErrChatNoActiveTurn }

func awaitWaitingOnInput(t *testing.T, h *harness, want bool) {
	t.Helper()
	controller, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatalf("Controller: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for controller.WaitingOnInput() != want {
		if time.Now().After(deadline) {
			t.Fatalf("WaitingOnInput never became %v", want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRelayedSendIsSteeredIntoATurnWaitingOnAQuestion(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"[from orch-1] use option B", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageDelivered || !receipt.WaitingOnInput || receipt.ID == "" {
		t.Fatalf("receipt = %+v, want delivered into the waiting turn with an activity id", receipt)
	}
	calls := provider.steers()
	if len(calls) != 1 || calls[0].turnID != "provider-turn-1" || calls[0].msg.Text != "[from orch-1] use option B" {
		t.Fatalf("steers = %+v, want the message steered into provider-turn-1", calls)
	}
	if calls[0].msg.Origin != domain.MessageOriginAutomation {
		t.Fatalf("steer origin = %q, want automation", calls[0].msg.Origin)
	}
	// Nothing was left queued behind the blocked turn.
	if sent := provider.sentTexts(); len(sent) != 1 {
		t.Fatalf("provider turns = %v, want only the original turn", sent)
	}
}

func TestRelayedSendQueuesNormallyWhenTheQuestionWasAnswered(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)
	provider.emit(ports.ChatEvent{
		Kind: ports.ChatEventInputResolved, ProviderTurnID: "provider-turn-1", RequestID: "question-request-1",
	})
	awaitWaitingOnInput(t, h, false)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"one more thing", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	// The turn is still running, so the message is steered into it like any
	// relay to a busy turn, but no longer flagged as answering a question.
	if receipt.Outcome != ports.MessageDelivered || receipt.WaitingOnInput {
		t.Fatalf("receipt = %+v, want an ordinary steer into the running turn", receipt)
	}
	if calls := provider.steers(); len(calls) != 1 || calls[0].turnID != "provider-turn-1" {
		t.Fatalf("steers = %+v, want one into provider-turn-1", calls)
	}
}

func TestRelayedSendWithAClientMessageIDKeepsTheDurableQueue(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"retried outbox message", "outbox-1", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageQueued || !receipt.WaitingOnInput {
		t.Fatalf("receipt = %+v, want queued and flagged as waiting on input", receipt)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("steers = %+v, want none for an idempotent retry", calls)
	}
}

func TestRelayedSendToAProviderThatCannotSteerSaysItIsStuckBehindTheQuestion(t *testing.T) {
	t.Parallel()

	provider := newFakeConversation() // no steering capability
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"use option B", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageQueued || !receipt.WaitingOnInput || receipt.ID == "" {
		t.Fatalf("receipt = %+v, want queued and flagged as waiting on input", receipt)
	}
}

func TestTurnEndClearsOpenQuestions(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1"})
	awaitWaitingOnInput(t, h, false)
}

// Stop is the natural way out of a turn blocked on a form, and it ends that
// turn without the completion that clears its question. The next turn must
// not inherit the question: a send to it is an ordinary queued message.
func TestQuestionOfAStoppedTurnDoesNotMarkTheNextTurnWaiting(t *testing.T) {
	t.Parallel()

	provider := &forgetfulOnStop{newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)
	ctx := context.Background()

	// The provider no longer knows the turn, so Tallpine settles it itself and no
	// completion for it ever clears the question.
	if err := h.svc.Interrupt(ctx, testSession); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	awaitWaitingOnInput(t, h, false)
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "start over", ClientMessageID: "turn-2", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send next turn: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2"})
	controller, err := h.svc.Controller(testSession)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(provider.sentTexts()) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("next turn never dispatched: %v", provider.sentTexts())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if controller.WaitingOnInput() {
		t.Fatal("the stopped turn's question marks the next turn as waiting")
	}

	receipt, err := h.svc.RelaySessionChatTurnReceipt(ctx, testSession,
		"[from orch-1] status?", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageDelivered || receipt.WaitingOnInput {
		t.Fatalf("receipt = %+v, want an ordinary steer, not one answering a question", receipt)
	}
	if calls := provider.steers(); len(calls) != 1 || calls[0].turnID != "provider-turn-2" {
		t.Fatalf("steers = %+v, want one into provider-turn-2", calls)
	}
}
