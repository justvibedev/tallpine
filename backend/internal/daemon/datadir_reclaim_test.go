package daemon

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/datadirlock"
)

const reclaimHolderEnv = "TALLPINE_TEST_RECLAIM_HOLDER"

// startLockHolder runs this test binary as a separate process that takes the
// data-dir lock and hangs like a daemon stuck in its shutdown. With stopBy set
// it marks the lock as stopping by that time.
func startLockHolder(t *testing.T, test, dir string, stopBy time.Time) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+test+"$")
	cmd.Env = append(os.Environ(), reclaimHolderEnv+"="+dir, fmt.Sprintf("TALLPINE_TEST_RECLAIM_STOP_BY=%d", stopBy.UnixMilli()))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		t.Fatalf("holder reported %q (%v), want locked", line, err)
	}
	return cmd
}

// runLockHolder is the child side of startLockHolder; it returns false in the
// parent test process.
func runLockHolder() bool {
	dir := os.Getenv(reclaimHolderEnv)
	if dir == "" {
		return false
	}
	lock, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
	if ms, _ := strconv.ParseInt(os.Getenv("TALLPINE_TEST_RECLAIM_STOP_BY"), 10, 64); ms > 0 {
		if err := lock.MarkStopping(time.UnixMilli(ms)); err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
	}
	fmt.Println("locked")
	_, _ = io.Copy(io.Discard, os.Stdin) // hang until killed or the parent dies
	runtime.KeepAlive(lock)
	os.Exit(0)
	return true
}

// waitExit fails the test unless cmd exits within a few seconds.
func waitExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("hung holder still running after the reclaim")
	}
}

func reclaimLog() (*bytes.Buffer, *slog.Logger) {
	var logs bytes.Buffer
	return &logs, slog.New(slog.NewTextHandler(&logs, nil))
}

// A predecessor still holding the lock past the stop-by time it recorded has
// outlived its own backstop: the replacement kills it and starts (B564).
func TestLockDataDirKillsHolderHungPastItsDeadline(t *testing.T) {
	if runLockHolder() {
		return
	}
	dir := t.TempDir()
	holder := startLockHolder(t, "TestLockDataDirKillsHolderHungPastItsDeadline", dir, time.Now().Add(-time.Minute))
	logs, log := reclaimLog()
	lock, err := lockDataDir(context.Background(), dir, "run.json", 200*time.Millisecond, log)
	if err != nil || lock == nil {
		t.Fatalf("lockDataDir = %v, %v; want the reclaimed lock\n%s", lock, err, logs)
	}
	defer func() { _ = lock.Release() }()
	waitExit(t, holder)
	if !strings.Contains(logs.String(), "data dir holder is hung past its shutdown deadline; killing it") {
		t.Fatalf("no kill log line in:\n%s", logs)
	}
}

// A predecessor that only just began stopping (a short TALLPINE_SHUTDOWN_TIMEOUT,
// or a wait that started before the quit) is not killed while its shutdown may
// still finish: the replacement waits for its stop-by time first.
func TestLockDataDirWaitsOutStoppingHolderDeadline(t *testing.T) {
	if runLockHolder() {
		return
	}
	dir := t.TempDir()
	// The lock file records stop-by in whole milliseconds, so compare against
	// the same truncated instant; the sub-millisecond remainder is not on disk.
	stopBy := time.Now().Add(time.Second).Truncate(time.Millisecond)
	holder := startLockHolder(t, "TestLockDataDirWaitsOutStoppingHolderDeadline", dir, stopBy)
	logs, log := reclaimLog()
	var killedAt time.Time
	prev := killProcess
	killProcess = func(pid int) error { killedAt = time.Now(); return prev(pid) }
	t.Cleanup(func() { killProcess = prev })
	lock, err := lockDataDir(context.Background(), dir, "run.json", 100*time.Millisecond, log)
	if err != nil || lock == nil {
		t.Fatalf("lockDataDir = %v, %v; want the reclaimed lock\n%s", lock, err, logs)
	}
	defer func() { _ = lock.Release() }()
	waitExit(t, holder)
	if earliest := stopBy.Add(reclaimMargin); killedAt.IsZero() || killedAt.Before(earliest) {
		t.Fatalf("holder killed at %s, want no earlier than %s (stop-by plus margin)", killedAt, earliest)
	}
	if !strings.Contains(logs.String(), "still within its shutdown deadline; waiting for it") {
		t.Fatalf("no waiting log line in:\n%s", logs)
	}
}

// A holder that never started shutting down may be a healthy daemon on the
// same data dir with its own run file (#3716), and a stop-by time beyond any
// backstop comes from a skewed clock: neither is killed, and the replacement
// refuses to start, as before.
func TestLockDataDirLeavesHolderRunning(t *testing.T) {
	if runLockHolder() {
		return
	}
	for name, tc := range map[string]struct {
		stopBy time.Time
		log    string
	}{
		"unmarked":   {log: "has not started shutting down; leaving it running"},
		"far future": {stopBy: time.Now().Add(time.Hour), log: "stop deadline is too far off; leaving it running"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			startLockHolder(t, "TestLockDataDirLeavesHolderRunning", dir, tc.stopBy)
			logs, log := reclaimLog()
			_, err := lockDataDir(context.Background(), dir, "run.json", 200*time.Millisecond, log)
			if !errors.Is(err, datadirlock.ErrLocked) || !strings.Contains(err.Error(), "refusing to start") {
				t.Fatalf("lockDataDir = %v, want the ErrLocked refusal", err)
			}
			if _, err := datadirlock.Acquire(context.Background(), dir, 0); !errors.Is(err, datadirlock.ErrLocked) {
				t.Fatalf("holder lost the lock: %v", err)
			}
			if !strings.Contains(logs.String(), tc.log) {
				t.Fatalf("no %q log line in:\n%s", tc.log, logs)
			}
		})
	}
}
