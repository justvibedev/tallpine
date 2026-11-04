package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Interaction of batching with steering (#75): a relayed send to a turn blocked
// on an in-chat question is steered, and the messages already waiting behind
// that turn ride the same provider input instead of staying stuck.

func queueHuman(t *testing.T, h *harness, text string) {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: text, ClientMessageID: text, Origin: domain.MessageOriginHuman,
	})
	if err != nil || turn.State != domain.TurnStateQueued {
		t.Fatalf("queue %q: turn=%+v err=%v", text, turn, err)
	}
}

func queuedCount(t *testing.T, h *harness) int {
	t.Helper()
	batch, err := h.st.ListQueuedBatch(context.Background(), h.ctrl.ConversationID(), 0)
	if errors.Is(err, domain.ErrNoQueuedTurn) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(batch)
}

func TestSendWhileBlockedOnQuestionSteersWithPendingBatch(t *testing.T) {
	provider := newSteerRecorder()
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)
	queueHuman(t, h, "earlier note")

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"use option B", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != ports.MessageDelivered || !receipt.WaitingOnInput {
		t.Fatalf("receipt = %+v", receipt)
	}
	calls := provider.steers()
	if len(calls) != 1 {
		t.Fatalf("steers = %+v, want one", calls)
	}
	items := parseDeliveredBatch(t, calls[0].msg.Text)
	if len(items) != 2 || items[0].Text != "earlier note" || items[1].Kind != "steer" || items[1].Text != "use option B" {
		t.Fatalf("steer batch = %+v", items)
	}
	if n := queuedCount(t, h); n != 0 {
		t.Fatalf("%d messages left queued behind the blocked turn", n)
	}
	if sent := provider.sentTexts(); len(sent) != 1 {
		t.Fatalf("provider turns = %v, want only the original", sent)
	}
}

func TestBlockedQuestionSteerRefusalKeepsEveryMessageQueuedOnce(t *testing.T) {
	provider := newSteerRecorder()
	provider.err = ports.ErrChatTurnNotSteerable // definitive refusal after the batch was claimed
	h := newHarnessWithConversation(t, provider)
	openQuestion(t, h, provider)
	queueHuman(t, h, "earlier note")

	receipt, err := h.svc.RelaySessionChatTurnReceipt(context.Background(), testSession,
		"use option B", "", ports.MessageDeliveryOptions{SenderSessionID: "orch-1"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != ports.MessageQueued || !receipt.WaitingOnInput {
		t.Fatalf("receipt = %+v", receipt)
	}
	// Late rather than dropped or duplicated: both wait, in order, exactly once.
	if n := queuedCount(t, h); n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}
	next, err := h.st.NextQueuedTurn(context.Background(), h.ctrl.ConversationID())
	if err != nil || next.Text != "earlier note" {
		t.Fatalf("queue head = %+v err=%v", next, err)
	}
}

func TestRestartWithPendingBatchDeliversItOnceAndNeverReplaysAnUncertainOne(t *testing.T) {
	h, _ := steerHarness(t)
	ctx := context.Background()
	queueHuman(t, h, "pending one")
	queueHuman(t, h, "pending two")
	// A steer that reserved a batch and then lost the daemon before recording
	// the receipt: Tallpine cannot know whether the provider took it.
	reserved, err := h.st.ClaimQueuedBatch(ctx, h.ctrl.ConversationID(), false, 1, h.now())
	if err != nil || len(reserved) != 1 || reserved[0].Text != "pending one" {
		t.Fatalf("reserved = %+v err=%v", reserved, err)
	}

	restartedProvider := newSteerRecorder()
	restartedProvider.turnSeq = 100 // a fresh provider process issues fresh turn ids
	restarted := crashRestart(t, h, restartedProvider)
	// The reservation is settled visibly, not left stranded as queued-but-unsendable.
	snap, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range snap.Turns {
		if tr.ID == reserved[0].TurnID && tr.State != domain.TurnStateFailed {
			t.Fatalf("uncertain reserved message state = %s, want failed", tr.State)
		}
	}
	// The next message drains the surviving queue as one input.
	if _, err := restarted.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "pending three", ClientMessageID: "pending three", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool { return len(restartedProvider.sentTexts()) >= 1 })
	sent := restartedProvider.sentTexts()
	if len(sent) != 1 {
		t.Fatalf("restart delivery = %v, want one provider input", sent)
	}
	items := parseDeliveredBatch(t, sent[0])
	if len(items) != 2 || items[0].Text != "pending two" || items[1].Text != "pending three" {
		t.Fatalf("restart batch = %+v (the reserved message must not be redelivered)", items)
	}
	if n := queuedCount(t, h); n != 0 {
		t.Fatalf("%d messages still queued after restart", n)
	}
}

// crashRestart starts a fresh service over the same store without stopping the
// old one: a daemon that died leaves its queue exactly as it was, whereas Stop
// is a graceful end that settles the queued turns.
func crashRestart(t *testing.T, h *harness, provider *steerRecorder) *chatsvc.Service {
	t.Helper()
	var (
		mu sync.Mutex
		n  int
	)
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			mu.Lock()
			defer mu.Unlock()
			n++
			return fmt.Sprintf("crash-restart-%d", n)
		},
		Now: h.now,
	})
	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1",
	}); err != nil {
		t.Fatalf("restart service: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	return svc
}
