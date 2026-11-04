package chat_test

import (
	"context"
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
