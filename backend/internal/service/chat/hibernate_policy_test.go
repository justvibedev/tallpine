package chat_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func setSessionKind(t *testing.T, h *harness, kind domain.SessionKind) {
	t.Helper()
	ctx := context.Background()
	rec, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("get session = %v, %v", found, err)
	}
	rec.Kind = kind
	if err := h.st.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
}

func asleep(t *testing.T, h *harness) bool {
	t.Helper()
	rec, _, err := h.st.GetSession(context.Background(), testSession)
	if err != nil {
		t.Fatal(err)
	}
	return rec.HibernatedAt != nil
}

func TestIdleHibernationWaitsForTheKindThreshold(t *testing.T) {
	t.Parallel()

	policy := func(_ context.Context, rec domain.SessionRecord) (time.Duration, bool) {
		return domain.DefaultChatHibernationPolicy().IdleFor(rec.Kind), true
	}
	for _, tc := range []struct {
		kind      domain.SessionKind
		threshold time.Duration
	}{
		{domain.KindWorker, 20 * time.Minute},
		{domain.KindOrchestrator, time.Hour},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
				opts.HibernationEnabled = func() bool { return true }
				opts.HibernationPolicy = policy
			})
			setSessionKind(t, h, tc.kind)
			ctx := context.Background()
			// The harness has already advanced 30s past the last activity.
			h.advance(tc.threshold - 31*time.Second)
			if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || slept || conv.calls.Load() != 0 {
				t.Fatalf("slept before %s: slept=%v err=%v", tc.threshold, slept, err)
			}
			if got := conv.backgroundChecks.Load(); got != 0 {
				t.Fatalf("checked provider work %d times before the threshold", got)
			}
			h.advance(time.Second)
			if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept || !asleep(t, h) {
				t.Fatalf("did not sleep at %s: slept=%v err=%v", tc.threshold, slept, err)
			}
		})
	}
}

func TestIdleHibernationHonorsProjectOptOut(t *testing.T) {
	t.Parallel()

	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return true }
		opts.HibernationPolicy = func(context.Context, domain.SessionRecord) (time.Duration, bool) { return 0, false }
	})
	h.advance(48 * time.Hour)
	if slept, err := h.svc.HibernateChat(context.Background(), testSession); err != nil || slept || conv.calls.Load() != 0 || asleep(t, h) {
		t.Fatalf("opted-out project slept: slept=%v err=%v", slept, err)
	}
}

func TestBackgroundProcessesKeepAgentAwake(t *testing.T) {
	t.Parallel()

	var busy atomic.Bool
	var probeErr atomic.Pointer[error]
	var probes atomic.Int32
	h, conv := hibernationHarnessWith(t, domain.TurnStateCompleted, func(opts *chatsvc.Options) {
		opts.HibernationEnabled = func() bool { return true }
		opts.BackgroundProcesses = func(_ context.Context, id domain.SessionID) (bool, error) {
			probes.Add(1)
			if id != testSession {
				t.Errorf("probed %s", id)
			}
			if err := probeErr.Load(); err != nil {
				return false, *err
			}
			return busy.Load(), nil
		}
	})
	ctx := context.Background()

	// A worker waiting on its own background build must not sleep.
	busy.Store(true)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || slept || conv.calls.Load() != 0 || asleep(t, h) || !h.svc.HasLiveChatController(testSession) {
		t.Fatalf("slept with background processes: slept=%v err=%v", slept, err)
	}
	// A process table it cannot read counts as busy.
	busy.Store(false)
	unreadable := errors.New("ps failed")
	probeErr.Store(&unreadable)
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || slept || conv.calls.Load() != 0 || asleep(t, h) {
		t.Fatalf("slept with an unreadable process table: slept=%v err=%v", slept, err)
	}
	// The provider's own registry is asked first; busy there skips the probe.
	probeErr.Store(nil)
	conv.backgroundRunning = true
	before := probes.Load()
	if slept, _ := h.svc.HibernateChat(ctx, testSession); slept || probes.Load() != before {
		t.Fatalf("probed processes while the provider reported work: slept=%v probes=%d->%d", slept, before, probes.Load())
	}
	conv.backgroundRunning = false
	// Once the work ends, the agent sleeps.
	if slept, err := h.svc.HibernateChat(ctx, testSession); err != nil || !slept || conv.calls.Load() != 1 || !asleep(t, h) {
		t.Fatalf("did not sleep after background work ended: slept=%v err=%v", slept, err)
	}
}
