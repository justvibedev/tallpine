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

func TestWakeIdleSkipsWorkersWhoseHostSurvived(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("kept", domain.ActivityIdle, false), worker("gone", domain.ActivityIdle, false))
	seedLostJobs(tr, "kept", "gone")
	snd := &fakeSender{}
	n := tr.WakeIdle(ctx, snd, func(id domain.SessionID) bool { return id == "kept" })
	if n != 1 || len(snd.sent["kept"]) != 0 || len(snd.sent["gone"]) != 1 {
		t.Fatalf("woke %d, sent=%v: a surviving host must not be told its jobs stopped", n, snd.sent)
	}
}

func TestWakeIdleRetriesAfterFailedSend(t *testing.T) {
	ctx := context.Background()
	tr, _ := newTracker(t, worker("w-1", domain.ActivityIdle, false))
	seedLostJobs(tr, "w-1")
	snd := &fakeSender{failed: 1}
	if n := tr.WakeIdle(ctx, snd, nil); n != 0 {
		t.Fatalf("failed send counted as woken: %d", n)
	}
	if n := tr.WakeIdle(ctx, snd, nil); n != 1 {
		t.Fatalf("retry woke %d, want 1", n)
	}
}

func TestWakeIdleSkipsTUIWorkers(t *testing.T) {
	tui := worker("tui", domain.ActivityIdle, false)
	tui.Mode = domain.SessionModeTUI
	tr, _ := newTracker(t, tui)
	seedLostJobs(tr, "tui")
	snd := &fakeSender{}
	if n := tr.WakeIdle(context.Background(), snd, nil); n != 0 || len(snd.sent) != 0 {
		t.Fatalf("woke %d, sent=%v: a TUI worker's terminal host survives, its jobs still run", n, snd.sent)
	}
}
