package daemon

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/datadirlock"
)

// A daemon that finds its data dir owned logs that it is waiting, then gives
// up after the bound with ErrLocked instead of opening ao.db.
func TestLockDataDirWaitsThenRefusesToStart(t *testing.T) {
	dir := t.TempDir()
	held, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	var logs bytes.Buffer
	const wait = 200 * time.Millisecond
	start := time.Now()
	_, err = lockDataDir(context.Background(), dir, "run.json", wait, slog.New(slog.NewTextHandler(&logs, nil)))
	if !errors.Is(err, datadirlock.ErrLocked) {
		t.Fatalf("lockDataDir = %v, want ErrLocked", err)
	}
	if elapsed := time.Since(start); elapsed < wait {
		t.Fatalf("gave up after %s, want at least %s", elapsed, wait)
	}
	if !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("error should say the daemon refuses to start: %v", err)
	}
	if !strings.Contains(logs.String(), "waiting for another Tallpine process to release the data dir") {
		t.Fatalf("no waiting log line in:\n%s", logs.String())
	}
}

// Only a lock that is still held gets the refusal verdict; any other failure
// while waiting (here a cancelled context) comes back as itself.
func TestLockDataDirReturnsOtherErrorsAsIs(t *testing.T) {
	dir := t.TempDir()
	held, err := datadirlock.Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = lockDataDir(ctx, dir, "run.json", time.Minute, slog.New(slog.DiscardHandler))
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("lockDataDir = %v, want context.Canceled without the lock verdict", err)
	}
}

// Every start records the data dir, run file, lock path and inode, and pid, so a second daemon that
// survived an install can be told apart afterwards (B469).
func TestLockDataDirLogsStartIdentity(t *testing.T) {
	dir := t.TempDir()
	var logs bytes.Buffer
	lock, err := lockDataDir(context.Background(), dir, "/run/x.json", 0, slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("lockDataDir: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })
	_, ino := datadirlock.Describe(dir)
	for _, want := range []string{"daemon start", "data_dir=" + dir, "run_file=/run/x.json", "lock_path=" + dir, "guarded=true", "pid="} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("start log missing %q in:\n%s", want, logs.String())
		}
	}
	if ino == 0 && runtime.GOOS != "windows" {
		t.Errorf("lock inode not reported")
	}
}
