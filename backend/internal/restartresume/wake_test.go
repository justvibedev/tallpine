package restartresume

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestWakeIdlePrunesStaleWakeRecords(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w-live", domain.ActivityActive, false), worker("w-old", domain.ActivityActive, false), worker("w-dead", domain.ActivityIdle, true))
	now := time.Now()
	tr.saveWake(map[domain.SessionID]time.Time{
		"w-live": now.Add(-time.Hour), "w-old": now.Add(-2 * maxAge), "w-dead": now.Add(-time.Minute), "w-gone": now.Add(-time.Minute),
	})
	tr.WakeIdle(ctx, &fakeSender{}, nil)
	got := tr.loadWake()
	if len(got) != 2 || got["w-live"].IsZero() || got["w-old"].IsZero() {
		t.Fatalf("wake file = %v, want w-live and w-old (old but unanswered) kept", got)
	}
}
