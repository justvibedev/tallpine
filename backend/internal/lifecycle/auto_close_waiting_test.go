package lifecycle

import (
	"testing"
	"time"
)

// B720: a worker whose last report says it waits on the owner is not closed.
func TestAutoClose_WaitingOnOwnerHoldsClose(t *testing.T) {
	m, term, hooks := autoCloseSetup(idleWorker("ac-wait"))
	var fire []func()
	m.autoCloseRetry.after = func(_ time.Duration, f func()) { fire = append(fire, f) }
	hooks.waiting = `last report is "needs_input"`
	observeMerged(t, m, "ac-wait")
	if term.calls != 0 || len(fire) != 1 {
		t.Fatalf("kills=%d timers=%d, want held with a recheck armed", term.calls, len(fire))
	}
	hooks.waiting = ""
	fire[0]()
	if term.calls != 1 {
		t.Fatalf("kills=%d after the wait ended, want 1", term.calls)
	}
}
