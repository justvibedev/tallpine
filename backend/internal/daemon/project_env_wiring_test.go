package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestMigrateProjectEnvLoop(t *testing.T) {
	var on atomic.Bool
	var polls atomic.Int32 // hardened() reads, one per loop iteration
	hardened := func() bool { polls.Add(1); return on.Load() }
	var calls, fails atomic.Int32
	fails.Store(2)
	migrate := func(context.Context) (int, error) {
		calls.Add(1)
		if fails.Add(-1) >= 0 {
			return 0, errors.New("locked")
		}
		return 1, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	go migrateProjectEnvLoop(ctx, hardened, migrate, time.Millisecond, log)

	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("migrated while the profile was off")
	}
	on.Store(true) // profile turns on after start
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 3 {
		t.Fatalf("calls = %d, want 2 retried failures then success", calls.Load())
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 3 {
		t.Fatalf("kept migrating after success: %d", calls.Load())
	}
	on.Store(false)
	// Wait until the loop has polled at least twice, so one poll saw it off.
	seen := polls.Load()
	offDeadline := time.Now().Add(2 * time.Second)
	for polls.Load() < seen+2 && time.Now().Before(offDeadline) {
		time.Sleep(time.Millisecond)
	}
	on.Store(true) // off then on re-arms the pass
	rearmDeadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 4 && time.Now().Before(rearmDeadline) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 4 {
		t.Fatalf("no pass after profile off/on: %d", calls.Load())
	}
}
