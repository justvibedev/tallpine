package daemon

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/datadirlock"
)

func stubExit(t *testing.T) <-chan int {
	t.Helper()
	exited := make(chan int, 1)
	prev := exitProcess
	exitProcess = func(code int) { exited <- code }
	t.Cleanup(func() { exitProcess = prev })
	return exited
}

// A shutdown that outlives its deadline is forced out with exit 1 after the
// goroutine stacks are saved next to daemon.log (B564).
func TestShutdownBackstopForcesExitAndDumpsGoroutines(t *testing.T) {
	exited := stubExit(t)
	dir := t.TempDir()
	var logs bytes.Buffer
	timer := armShutdownBackstop(20*time.Millisecond, dir, slog.New(slog.NewTextHandler(&logs, nil)))
	defer timer.Stop()
	select {
	case code := <-exited:
		if code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backstop never forced the exit")
	}
	dump, err := os.ReadFile(shutdownGoroutineDumpPath(dir))
	if err != nil || !strings.Contains(string(dump), "goroutine ") {
		t.Fatalf("goroutine dump = %q, %v", dump, err)
	}
	if !strings.Contains(logs.String(), "daemon shutdown hung past its deadline; forcing exit") {
		t.Fatalf("no backstop log line in:\n%s", logs.String())
	}
}

// Run disarms the backstop once it is done, so a clean exit is never forced.
func TestShutdownBackstopDisarmed(t *testing.T) {
	exited := stubExit(t)
	b := &boot{shutdownBackstop: armShutdownBackstop(50*time.Millisecond, t.TempDir(), slog.New(slog.DiscardHandler))}
	b.disarmShutdownBackstop()
	select {
	case code := <-exited:
		t.Fatalf("disarmed backstop exited %d", code)
	case <-time.After(300 * time.Millisecond):
	}
	(&boot{}).disarmShutdownBackstop() // never armed: no-op
}

// From T=2s up the backstop fires before a takeover's replacement stops
// waiting for the lock: the drain (at most T) plus the deadline stays inside
// 9T+fixed. Below that the 30s floor wins, and the replacement waits on for
// the stop-by time in the lock file instead of killing (reclaim tests).
func TestShutdownBackstopBeatsReplacementLockWait(t *testing.T) {
	for T := 2 * time.Second; T <= 2*time.Minute; T += 100 * time.Millisecond {
		if got, wait := T+shutdownBackstopDeadline(T), 9*T+dataDirLockFixedWait; got >= wait {
			t.Fatalf("T=%s: drain+backstop %s does not beat the replacement's lock wait %s", T, got, wait)
		}
	}
	for T := time.Duration(0); T <= 2*time.Minute; T += 100 * time.Millisecond {
		// Whatever the backstop, a replacement's cap on waiting for a stop-by
		// time covers a holder that only began stopping as its own wait ended.
		if need, capped := shutdownBackstopDeadline(T)+shutdownBackstopGrace+reclaimMargin, reclaimMaxStopWait(9*T+dataDirLockFixedWait); need > capped {
			t.Fatalf("T=%s: holder may need %s past the wait, cap is %s", T, need, capped)
		}
	}
	if got := shutdownBackstopDeadline(100 * time.Millisecond); got != shutdownBackstopFloor {
		t.Errorf("tiny T backstop = %s, want the %s floor", got, shutdownBackstopFloor)
	}
}

// beginShutdown, the first step of shutdownAfterRun, arms the backstop and
// marks the held data-dir lock as stopping.
func TestBeginShutdownArmsBackstopAndMarksLock(t *testing.T) {
	dir := t.TempDir()
	lock, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer func() { _ = lock.Release() }()
	b := &boot{cfg: config.Config{DataDir: dir, ShutdownTimeout: time.Second}, log: slog.New(slog.DiscardHandler), dataDirLock: lock}
	b.beginShutdown()
	defer b.disarmShutdownBackstop()
	if b.shutdownBackstop == nil {
		t.Fatal("beginShutdown did not arm the backstop")
	}
	want := time.Now().Add(shutdownBackstopDeadline(time.Second) + shutdownBackstopGrace)
	if got := datadirlock.ReadHolder(dir).StopBy; got.IsZero() || got.Sub(want).Abs() > 2*time.Second {
		t.Fatalf("lock marked stopping by %s, want about %s (backstop plus grace)", got, want)
	}
	(&boot{cfg: b.cfg, log: b.log}).beginShutdown() // unguarded (nil lock): no panic
}
