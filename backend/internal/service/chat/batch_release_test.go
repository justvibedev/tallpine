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

// faultStore fails chosen writes that happen before the provider can have seen
// anything, so a claimed batch must go back to the queue rather than fail.
type faultStore struct {
	chatsvc.Store
	failAppend atomic.Bool
	failBind   atomic.Bool
}

func (s *faultStore) AppendUserMessage(ctx context.Context, conv string, session domain.SessionID, gen string, msg domain.ConversationMessage, turnID string, now time.Time) (bool, error) {
	if s.failAppend.Load() {
		return false, errors.New("injected append failure")
	}
	return s.Store.AppendUserMessage(ctx, conv, session, gen, msg, turnID, now)
}

func (s *faultStore) BindPrimaryChatTurnToProvider(ctx context.Context, turnID, providerTurnID, gen string, now time.Time) error {
	if s.failBind.Load() {
		return errors.New("injected bind failure")
	}
	return s.Store.BindPrimaryChatTurnToProvider(ctx, turnID, providerTurnID, gen, now)
}

func seedQueuedHuman(t *testing.T, h *harness, texts ...string) {
	t.Helper()
	record, found, err := h.st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	for i, text := range texts {
		if created, err := h.st.AppendUserMessage(context.Background(), h.ctrl.ConversationID(), testSession, record.Metadata.ControllerGeneration,
			domain.ConversationMessage{ID: "seed-m-" + text, Text: text, ClientMessageID: text, Origin: domain.MessageOriginHuman},
			"seed-t-"+text, h.now().Add(time.Duration(i)*time.Second)); err != nil || !created {
			t.Fatalf("seed %q: created=%v err=%v", text, created, err)
		}
	}
}

func TestSteerOrSendPreDispatchFailureReleasesPendingBatch(t *testing.T) {
	var faults *faultStore
	fake := newFakeConversation()
	h := newHarnessWithConversationAndStore(t, fake, func(st *sqlite.Store) chatsvc.Store {
		faults = &faultStore{Store: st}
		return faults
	})
	ctx := context.Background()
	seedQueuedHuman(t, h, "pending one", "pending two")

	faults.failAppend.Store(true)
	if _, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "now", ClientMessageID: "now", Origin: domain.MessageOriginHuman,
	}, false); err == nil {
		t.Fatal("expected the injected failure")
	}
	// Nothing reached the provider: both stay queued, not failed.
	if n := queuedCount(t, h); n != 2 || len(fake.sentTexts()) != 0 {
		t.Fatalf("queued=%d sent=%v after pre-dispatch failure", n, fake.sentTexts())
	}

	faults.failAppend.Store(false)
	if res, err := h.svc.SteerOrSend(ctx, testSession, ports.ChatUserMessage{
		Text: "now", ClientMessageID: "now-retry", Origin: domain.MessageOriginHuman,
	}, false); err != nil {
		t.Fatal(err)
	} else if res.Turn.ID == "" {
		t.Fatalf("result = %+v", res)
	}
	sent := fake.sentTexts()
	if len(sent) != 1 {
		t.Fatalf("sent = %v, want one combined input", sent)
	}
	items := parseDeliveredBatch(t, sent[0])
	if len(items) != 3 || items[0].Text != "pending one" || items[1].Text != "pending two" {
		t.Fatalf("delivery = %+v, want each message exactly once", items)
	}
	if n := queuedCount(t, h); n != 0 {
		t.Fatalf("%d left queued", n)
	}
}

// ACP-style drivers send the prompt only in StartDeferredTurn. A bind failure
// before that means nothing was sent: the whole drained batch must be delivered
// by the next drain, once, and nothing is failed.
func TestDeferredDrainBindFailureReturnsBatchToQueue(t *testing.T) {
	var faults *faultStore
	conv := &deferredConversation{fakeConversation: newFakeConversation(), start: func(string) error { return nil }}
	h := newHarnessWithConversationAndStore(t, conv, func(st *sqlite.Store) chatsvc.Store {
		faults = &faultStore{Store: st}
		return faults
	})
	ctx := context.Background()
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", ClientMessageID: "active", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	queueHuman(t, h, "queued one")
	queueHuman(t, h, "queued two")

	faults.failBind.Store(true)
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	deadline := time.Now().Add(3 * time.Second)
	for len(conv.sentTexts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(conv.sentTexts()) != 2 {
		t.Fatal("completed turn did not attempt to drain the queue")
	}
	// SendTurn records the attempt before binding fails and releases the batch.
	// Wait for the completion event's post-commit drain before reading the queue.
	projectionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := h.ctrl.WaitProjection(projectionCtx); err != nil {
		t.Fatalf("wait for failed drain cleanup: %v", err)
	}
	if n := queuedCount(t, h); n != 2 {
		t.Fatalf("queued after bind failure = %d, want both back in the queue", n)
	}
	snap, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range snap.Turns {
		if tr.State == domain.TurnStateFailed {
			t.Fatalf("turn %s failed although nothing was sent: %+v", tr.ID, tr)
		}
	}

	faults.failBind.Store(false)
	// The next send drains the queue ahead of itself.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "queued three", ClientMessageID: "queued three", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatal(err)
	}
	var combined []string
	for _, text := range conv.sentTexts() {
		if text != "active" {
			combined = append(combined, text)
		}
	}
	last := combined[len(combined)-1]
	items := parseDeliveredBatch(t, last)
	if len(items) != 3 || items[0].Text != "queued one" || items[1].Text != "queued two" || items[2].Text != "queued three" {
		t.Fatalf("redelivery = %+v", items)
	}
	if n := queuedCount(t, h); n != 0 {
		t.Fatalf("%d left queued", n)
	}
}
