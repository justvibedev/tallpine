package chat_test

import (
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// RetryQueuedWakes picks due sessions before it starts them, so a wake that
// fails in between has already pushed its retry out: the stale pick must not
// start an attempt inside the backoff window, which deferWake would not count.
func TestRetryWakeSkipsSessionWhoseRetryIsNotDue(t *testing.T) {
	t.Parallel()

	h, old, svc, _ := sleepingSession(t, domain.KindWorker)
	var attempts atomic.Int32
	svc.SetWakeCallback(wakeAs(h, svc, old.ProviderConversationID(), &attempts, func(int32) bool { return false }))
	if !svc.DeferWake(testSession) {
		t.Fatal("first failure must schedule a retry")
	}
	svc.RunBackgroundWake(testSession, true)
	time.Sleep(100 * time.Millisecond)
	if n := attempts.Load(); n != 0 {
		t.Fatalf("retry inside its backoff window started %d attempts", n)
	}
	h.advance(time.Hour)
	svc.RunBackgroundWake(testSession, true)
	waitAttempts(t, &attempts, 1, nil)
}
