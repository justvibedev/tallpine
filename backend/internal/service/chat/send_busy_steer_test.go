package chat_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// A `tallpine send` to a worker mid-turn ("hold, don't push") must reach the
// running turn, not wait until the worker has already acted (B85 part 1).

func startBusyTurn(t *testing.T, h *harness, provider inputAsker) {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	awaitTurnState(t, h, turn.ID, domain.TurnStateRunning)
}

func TestRelayedSendIsSteeredIntoABusyTurn(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"[from orch-1] hold, do not push", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageDelivered || receipt.WaitingOnInput || receipt.ID == "" {
		t.Fatalf("receipt = %+v, want delivered into the running turn with an activity id", receipt)
	}
	calls := provider.steers()
	if len(calls) != 1 || calls[0].turnID != "provider-turn-1" || calls[0].msg.Text != "[from orch-1] hold, do not push" {
		t.Fatalf("steers = %+v, want the message steered into provider-turn-1", calls)
	}
	if sent := provider.sentTexts(); len(sent) != 1 {
		t.Fatalf("provider turns = %v, want only the original turn", sent)
	}
}

// Worker-originated relays carry a worker-send client id, and outbox retries
// carry their own; both keep the durable queue boundary.
func TestRelayedSendWithAClientIDIsNotSteeredIntoABusyTurn(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"worker report", "worker-send:abc", ports.MessageDeliveryOptions{})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageQueued {
		t.Fatalf("receipt = %+v, want queued", receipt)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("steers = %+v, want none", calls)
	}
}

func TestRelayedSendQueuesWhenTheBusyTurnRefusesSteering(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	provider.err = ports.ErrChatTurnNotSteerable
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"status?", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v", err)
	}
	if receipt.Outcome != ports.MessageQueued || receipt.ID == "" {
		t.Fatalf("receipt = %+v, want queued after the refusal", receipt)
	}
}

// A steer whose outcome is unknown may have reached the agent; queuing it too
// would deliver the guidance twice.
func TestRelayedSendDoesNotQueueAfterAnUncertainSteer(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	provider.err = errors.New("transport lost")
	h := newHarnessWithConversation(t, provider)
	startBusyTurn(t, h, provider)

	_, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"status?", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if !errors.Is(err, chatsvc.ErrSteerDeliveryUncertain) {
		t.Fatalf("err = %v, want ErrSteerDeliveryUncertain so the sender does not resend", err)
	}
	queued, err := h.st.ListQueuedBatch(context.Background(), h.ctrl.ConversationID(), 0)
	if err != nil && !errors.Is(err, domain.ErrNoQueuedTurn) {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("queued turns = %+v, want none", queued)
	}
}

// corruptBatchStore hands the steer a queued row whose stored content cannot be
// decoded, so composing the batch fails before anything reaches the provider.
type corruptBatchStore struct {
	chatsvc.Store
	corrupt atomic.Bool
}

func (s *corruptBatchStore) ClaimQueuedBatch(ctx context.Context, id string, dispatch bool, limit int, now time.Time) ([]domain.QueuedTurn, error) {
	batch, err := s.Store.ClaimQueuedBatch(ctx, id, dispatch, limit, now)
	if err == nil && s.corrupt.Load() {
		for i := range batch {
			batch[i].DeliveryContentJSON = "{not json"
		}
	}
	return batch, err
}

// A steer that fails before dispatch delivered nothing: the send is queued as
// before steering existed, not reported as possibly delivered.
func TestRelayedSendQueuesWhenTheSteerFailsBeforeDispatch(t *testing.T) {
	t.Parallel()

	provider := newSteerRecorder()
	var st *corruptBatchStore
	h := newHarnessWithConversationAndStore(t, provider, func(base *sqlite.Store) chatsvc.Store {
		st = &corruptBatchStore{Store: base}
		return st
	})
	startBusyTurn(t, h, provider)
	if _, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "queued earlier", ClientMessageID: "queued-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send queued: %v", err)
	}
	st.corrupt.Store(true)

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"hold, do not push", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatalf("RelaySessionChatTurnReceipt: %v, want the send queued", err)
	}
	if receipt.Outcome != ports.MessageQueued || receipt.ID == "" {
		t.Fatalf("receipt = %+v, want queued", receipt)
	}
	if calls := provider.steers(); len(calls) != 0 {
		t.Fatalf("steers = %+v, want none to reach the provider", calls)
	}
}
