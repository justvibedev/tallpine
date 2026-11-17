package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// B496: after a daemon restart a resume held the controller gate for ~11s
// (provider launch, native history import) and every `tallpine send` to that
// session waited it out (gateWait=10.8s/16.3s). A relayed send must not wait
// on a start: it is queued durably and delivered once the controller is up.
func TestRelayDuringSlowStartQueuesWithoutWaitingOnGate(t *testing.T) {
	t.Parallel()
	for name, relay := range map[string]func(context.Context, *chatsvc.Service, domain.SessionID) (string, error){
		"receipt": func(ctx context.Context, svc *chatsvc.Service, id domain.SessionID) (string, error) {
			receipt, err := svc.RelaySessionChatTurnReceipt(ctx, id, "hold, don't push", "", ports.MessageDeliveryOptions{})
			if err == nil && receipt.Outcome != ports.MessageQueued {
				return "", fmt.Errorf("outcome = %v, want queued", receipt.Outcome)
			}
			return receipt.ID, err
		},
		"turn": func(ctx context.Context, svc *chatsvc.Service, id domain.SessionID) (string, error) {
			return svc.RelaySessionChatTurn(ctx, id, "hold, don't push", "", ports.MessageDeliveryOptions{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			st, id := openProvisioningStore(t, domain.SessionProvisionReady)
			conv := newFakeConversation()
			entered, release := make(chan struct{}), make(chan struct{})
			var next int64
			var mu sync.Mutex
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st, Reader: fullSnapshotReader(st),
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv, start: func(ports.ChatStartConfig) (ports.ChatConversation, error) {
					close(entered)
					<-release
					return conv, nil
				}}},
				Log: slog.New(slog.DiscardHandler),
				NewID: func() string {
					mu.Lock()
					defer mu.Unlock()
					next++
					return fmt.Sprintf("id-%d", next)
				},
			})
			started := make(chan error, 1)
			go func() {
				_, err := svc.Start(context.Background(), chatsvc.StartConfig{
					SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
					Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
					ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
						return chatsvc.ControllerCommit{}, nil
					},
				})
				started <- err
			}()
			<-entered

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			turnID, err := relay(ctx, svc, id)
			if err != nil {
				close(release)
				<-started
				t.Fatalf("send during start: %v (want queued without waiting on the gate)", err)
			}
			if turnID == "" {
				t.Fatal("send during start returned no turn id")
			}
			close(release)
			if err := <-started; err != nil {
				t.Fatalf("start: %v", err)
			}
			t.Cleanup(func() { _ = svc.Stop(context.Background(), id) })
			deadline := time.Now().Add(5 * time.Second)
			for {
				conv.mu.Lock()
				sent := len(conv.sent)
				conv.mu.Unlock()
				if sent == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("queued send was not delivered after start (sent=%d)", sent)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

// The B496 case itself: the daemon parked the agent on quit, and the next
// daemon's wake was resuming it when the orchestrator was sent to. The relay is
// acknowledged as queued at once and delivered exactly once after the wake.
func TestRelayDuringSlowWakeQueuesAndDeliversOnce(t *testing.T) {
	t.Parallel()

	h, old, svc, resumed := sleepingSession(t, domain.KindOrchestrator)
	ctx := context.Background()
	var attempts atomic.Int32
	inWake, release := make(chan struct{}), make(chan struct{})
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool {
		close(inWake)
		<-release
		return false
	}))
	woke := make(chan error, 1)
	go func() { woke <- svc.SetChatView(ctx, testSession, "phone", true) }()
	<-inWake

	sendCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	receipt, err := svc.RelaySessionChatTurnReceipt(sendCtx, testSession, "hold, don't push", "", ports.MessageDeliveryOptions{})
	close(release)
	if err != nil || receipt.Outcome != ports.MessageQueued {
		t.Fatalf("relay during wake = %+v, %v; want queued without waiting", receipt, err)
	}
	<-woke
	h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(resumed.sentTexts()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if sent := resumed.sentTexts(); !slices.Equal(sent, []string{"hold, don't push"}) {
		t.Fatalf("delivered = %v, want the message exactly once", sent)
	}
}

// B770 follow-up: turns queued behind a usage-limit pause stay durable. When the
// pause ended while the daemon was down, no wake is re-armed on start, so Start
// itself must deliver them.
// endedPauseStore reports a usage-limit refusal whose reset has already passed.
type endedPauseStore struct{ chatsvc.Store }

func (endedPauseStore) LatestAutoResumeTurn(context.Context, string) (domain.ConversationTurn, error) {
	failed := time.Now().Add(-2 * time.Hour)
	reset := time.Now().Add(-time.Hour)
	return domain.ConversationTurn{State: domain.TurnStateFailed, CompletedAt: &failed,
		UsageLimit: &domain.ConversationUsageLimit{ResetsAt: &reset}}, nil
}

func TestStartDeliversTurnsQueuedBeforeStartWhenNotPaused(t *testing.T) {
	t.Parallel()
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	conv := newFakeConversation()
	var next int64
	var mu sync.Mutex
	svc := chatsvc.New(chatsvc.Options{
		Store: endedPauseStore{Store: st}, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			mu.Lock()
			defer mu.Unlock()
			next++
			return fmt.Sprintf("id-%d", next)
		},
	})
	ctx := context.Background()
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "after the pause", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, nil
		},
	}); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, id) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		conv.mu.Lock()
		sent := len(conv.sent)
		conv.mu.Unlock()
		if sent == 1 {
			time.Sleep(200 * time.Millisecond)
			conv.mu.Lock()
			defer conv.mu.Unlock()
			if len(conv.sent) != 1 {
				t.Fatalf("queued turn dispatched %d times, want once", len(conv.sent))
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queued turn not delivered by start (sent=%d)", sent)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A start that fails before the controller is committed must not dispatch the
// queued turn: it stays queued for the next start.
func TestFailedStartLeavesQueuedTurnAfterEndedPause(t *testing.T) {
	t.Parallel()
	st, id := openProvisioningStore(t, domain.SessionProvisionProvisioning)
	conv := newFakeConversation()
	var ids atomic.Int64
	svc := chatsvc.New(chatsvc.Options{
		Store: endedPauseStore{Store: st}, Sessions: st, Reader: fullSnapshotReader(st),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("f-%d", ids.Add(1)) },
	})
	ctx := context.Background()
	if _, err := svc.Send(ctx, id, ports.ChatUserMessage{Text: "held", Origin: domain.MessageOriginHuman}); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: id, ProjectID: testProject, Kind: domain.KindWorker,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(),
		ControllerReady: func(chatsvc.StartResult) (chatsvc.ControllerCommit, error) {
			return chatsvc.ControllerCommit{}, errors.New("ownership commit failed")
		},
	}); err == nil {
		t.Fatal("start succeeded, want the commit error")
	}
	time.Sleep(300 * time.Millisecond)
	conv.mu.Lock()
	sent := len(conv.sent)
	conv.mu.Unlock()
	if sent != 0 {
		t.Fatalf("sent = %d after a failed start, want 0", sent)
	}
	if _, err := st.NextQueuedTurn(ctx, mustConversationID(t, st, id)); err != nil {
		t.Fatalf("turn no longer queued: %v", err)
	}
}

func mustConversationID(t *testing.T, st interface {
	ConversationForSession(context.Context, domain.SessionID) (domain.ConversationRecord, error)
}, id domain.SessionID) string {
	t.Helper()
	c, err := st.ConversationForSession(context.Background(), id)
	if err != nil {
		t.Fatalf("conversation: %v", err)
	}
	return c.ID
}
