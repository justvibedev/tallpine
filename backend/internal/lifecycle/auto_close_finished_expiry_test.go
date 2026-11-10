package lifecycle

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B616: open linked items hold a finished worker only up to the bound; unmerged
// work (keep reason) still holds past it.
func TestAutoClose_FinishedWorkerItemsHoldExpires(t *testing.T) {
	m, term, hooks := finishedSetup(idleWorker("fw-exp"))
	now := time.Unix(1000, 0)
	m.autoCloseRetry.clock = func() time.Time { return now }
	hooks.items = 1
	hooks.sessions = []domain.SessionID{"fw-exp"}
	m.RearmAutoCloses(ctx)
	if term.calls != 0 {
		t.Fatalf("kills=%d before the bound, want hold", term.calls)
	}
	now = now.Add(autoCloseMaxHold - time.Minute)
	hooks.finishedKeep = "unmerged commits"
	m.RearmAutoCloses(ctx)
	now = now.Add(2 * time.Minute)
	hooks.finishedKeep = ""
	m.RearmAutoCloses(ctx)
	if term.calls != 0 {
		t.Fatalf("kills=%d after a keep reset the clock, want hold", term.calls)
	}
	now = now.Add(autoCloseMaxHold + time.Minute)
	m.RearmAutoCloses(ctx)
	if term.calls != 1 || len(hooks.finishedClosed) != 1 {
		t.Fatalf("kills=%d finished=%v past the bound, want close", term.calls, hooks.finishedClosed)
	}
}
