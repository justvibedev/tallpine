package chat_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

const enotfound = "API Error: Can't reach the API server — check your internet or DNS (ENOTFOUND)"

// startRetrying restarts the test session as kind on a service whose transient
// retry policy is set before the controller launches, as the daemon wires it.
func startRetrying(t *testing.T, kind domain.SessionKind, policy chatsvc.TransientRetryPolicy) *harness {
	t.Helper()
	h := newHarness(t)
	ctx := context.Background()
	if err := h.svc.Stop(ctx, testSession); err != nil {
		t.Fatal(err)
	}
	provider := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: h.st, Reader: fullSnapshotReader(h.st), PageReader: boundedSnapshotReader(h.st), Sessions: h.st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}}, NewID: uuid.NewString, Now: h.now,
	})
	svc.SetTransientRetryPolicy(&policy)
	controller, err := svc.Start(ctx, chatsvc.StartConfig{SessionID: testSession, ProjectID: testProject, Kind: kind,
		Harness: domain.HarnessCodex, WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	h.svc, h.ctrl, h.conv = svc, controller, provider
	return h
}

func fastPolicy(attempts int) chatsvc.TransientRetryPolicy {
	backoff := make([]time.Duration, attempts)
	for i := range backoff {
		backoff[i] = time.Millisecond
	}
	return chatsvc.TransientRetryPolicy{
		Backoff: backoff, OfflinePoll: 5 * time.Millisecond, MaxOffline: time.Hour,
		Online: func(context.Context, domain.AgentHarness) bool { return true },
	}
}

// sendAndFail sends a prompt and fails its provider turn with message.
func sendAndFail(t *testing.T, h *harness, message string) domain.ConversationTurn {
	t.Helper()
	turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "finish this task", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	failProviderTurn(t, h, turn.ProviderTurnID, message)
	return turn
}

func failProviderTurn(t *testing.T, h *harness, providerTurnID, message string) {
	t.Helper()
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: providerTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(message)})
}

func awaitSends(t *testing.T, h *harness, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.conv.sentMessages()) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("provider received %d messages, want %d", len(h.conv.sentMessages()), want)
}

// requireSendsStay fails if more than want messages reach the provider.
func requireSendsStay(t *testing.T, h *harness, want int) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if got := len(h.conv.sentMessages()); got != want {
		t.Fatalf("provider received %d messages, want exactly %d", got, want)
	}
}

func snapshotRetry(t *testing.T, h *harness, turnID string) *domain.ConversationTurnAutoRetry {
	t.Helper()
	snapshot, err := h.svc.Snapshot(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := findTurn(snapshot.Turns, turnID)
	if !ok {
		t.Fatalf("turn %s missing from snapshot", turnID)
	}
	return turn.AutoRetry
}

func TestTransientFailureRetriesWithoutOwnerInput(t *testing.T) {
	t.Parallel()
	for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
		t.Run(string(kind), func(t *testing.T) {
			t.Parallel()
			h := startRetrying(t, kind, fastPolicy(3))
			source := sendAndFail(t, h, enotfound)
			awaitSends(t, h, 2)
			if got := h.conv.sentMessages()[1].Text; got != "finish this task" {
				t.Fatalf("retry sent %q, want the failed prompt again", got)
			}
			retryID, found, err := h.st.RetryTurnIDForSource(context.Background(), h.ctrl.ConversationID(), source.ID)
			if err != nil || !found || retryID == "" {
				t.Fatalf("retry of %s = %q, %v, %v; want a durable retry turn", source.ID, retryID, found, err)
			}
			requireSendsStay(t, h, 2)
		})
	}
}

func TestTransientRetryStopsAtCap(t *testing.T) {
	t.Parallel()
	h := startRetrying(t, domain.KindWorker, fastPolicy(2))
	sendAndFail(t, h, enotfound)
	// Each retry fails transiently too: the original plus two retries, then stop.
	for attempt := 2; attempt <= 3; attempt++ {
		awaitSends(t, h, attempt)
		failProviderTurn(t, h, fmt.Sprintf("provider-turn-%d", attempt), "read ECONNRESET")
	}
	requireSendsStay(t, h, 3)
	last, err := h.st.TurnByProviderID(context.Background(), h.ctrl.ConversationID(), "provider-turn-3")
	if err != nil {
		t.Fatal(err)
	}
	awaitUsageLimitProjection(t, h.ctrl)
	if retry := snapshotRetry(t, h, last.ID); retry != nil {
		t.Fatalf("turn past the cap shows a pending retry %+v; want its error to surface", retry)
	}
}

func TestNonTransientFailureIsNotRetried(t *testing.T) {
	t.Parallel()
	h := startRetrying(t, domain.KindWorker, fastPolicy(3))
	source := sendAndFail(t, h, "API Error: 400 prompt is too long: 210000 tokens > 200000 maximum")
	requireSendsStay(t, h, 1)
	if retry := snapshotRetry(t, h, source.ID); retry != nil {
		t.Fatalf("non-transient failure shows a pending retry %+v", retry)
	}
}

func TestTransientRetryWaitsForNetworkWithoutSpendingAttempts(t *testing.T) {
	t.Parallel()
	var online atomic.Bool
	var probes atomic.Int32
	policy := fastPolicy(1)
	policy.Online = func(context.Context, domain.AgentHarness) bool {
		probes.Add(1)
		return online.Load()
	}
	h := startRetrying(t, domain.KindOrchestrator, policy)
	source := sendAndFail(t, h, enotfound)
	deadline := time.Now().Add(5 * time.Second)
	for probes.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(h.conv.sentMessages()); got != 1 {
		t.Fatalf("provider received %d messages while offline, want 1", got)
	}
	retry := snapshotRetry(t, h, source.ID)
	if retry == nil || !retry.Offline || retry.Attempt != 1 || retry.MaxAttempts != 1 {
		t.Fatalf("offline snapshot retry = %+v, want attempt 1/1 waiting offline", retry)
	}
	online.Store(true)
	awaitSends(t, h, 2) // the single attempt was still there once the network came back
}

func TestTransientRetryGivesUpAfterMaxOffline(t *testing.T) {
	t.Parallel()
	policy := fastPolicy(3)
	policy.MaxOffline = 0
	policy.Online = func(context.Context, domain.AgentHarness) bool { return false }
	h := startRetrying(t, domain.KindWorker, policy)
	source := sendAndFail(t, h, enotfound)
	requireSendsStay(t, h, 1)
	if retry := snapshotRetry(t, h, source.ID); retry != nil {
		t.Fatalf("abandoned retry still shown: %+v", retry)
	}
}

func TestTransientRetryShowsPendingAndYieldsToNewerTurn(t *testing.T) {
	t.Parallel()
	policy := fastPolicy(1)
	policy.Backoff = []time.Duration{200 * time.Millisecond}
	h := startRetrying(t, domain.KindWorker, policy)
	source := sendAndFail(t, h, enotfound)
	awaitUsageLimitProjection(t, h.ctrl)
	if retry := snapshotRetry(t, h, source.ID); retry == nil || retry.Attempt != 1 || retry.Offline {
		t.Fatalf("pending retry = %+v, want attempt 1 scheduled", retry)
	}
	// The paged read the HTTP route serves carries the same overlay.
	page, err := h.svc.SnapshotPage(context.Background(), testSession, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if turn, ok := findTurn(page.Turns, source.ID); !ok || turn.AutoRetry == nil {
		t.Fatalf("paged snapshot turn = %+v, want the pending retry overlaid", turn)
	}
	// A newer prompt that ran and finished before the retry fired supersedes it.
	newer, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{
		Text: "do something else", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: newer.ProviderTurnID,
		TurnState: domain.TurnStateCompleted})
	awaitUsageLimitProjection(t, h.ctrl)
	time.Sleep(300 * time.Millisecond)
	if texts := h.conv.sentTexts(); len(texts) != 2 || texts[1] != "do something else" {
		t.Fatalf("provider received %q, want the newer prompt and no retry", texts)
	}
}
