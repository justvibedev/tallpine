package process

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func startSleeper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("no sleep binary: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// A helper that never exits on its own must not block the caller past the
// timeout, and the root process is still killed (fallback).
func TestKillTreeWithHonoursTimeoutAndFallsBack(t *testing.T) {
	cmd := startSleeper(t)
	done := make(chan error, 1)
	go func() {
		done <- killTreeWith(context.Background(), cmd, 50*time.Millisecond, func(ctx context.Context, _ int) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("fallback kill failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("killTreeWith blocked past its timeout")
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("root process was not killed by the fallback")
	}
}

func TestKillTreeWithSkipsFallbackOnSuccess(t *testing.T) {
	cmd := startSleeper(t)
	if err := killTreeWith(context.Background(), cmd, time.Second, func(context.Context, int) error { return nil }); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case <-waited:
		t.Fatal("process killed although helper succeeded")
	case <-time.After(200 * time.Millisecond):
	}
	// Only this goroutine ever calls Wait: kill and drain its channel.
	_ = cmd.Process.Kill()
	<-waited
}

func TestKillTreeWithNilProcess(t *testing.T) {
	if err := killTreeWith(context.Background(), &exec.Cmd{}, time.Second, func(context.Context, int) error { return errors.New("x") }); err != nil {
		t.Fatal(err)
	}
}

func TestKillTreeWithHonoursParentContext(t *testing.T) {
	cmd := startSleeper(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = killTreeWith(ctx, cmd, time.Hour, func(ctx context.Context, _ int) error {
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parent context cancellation ignored")
	}
}

func TestKillPIDWithBoundsHelperAndPassesForce(t *testing.T) {
	var gotForce bool
	err := killPIDWith(42, true, 50*time.Millisecond, func(ctx context.Context, pid int, force bool) error {
		gotForce = force && pid == 42
		<-ctx.Done()
		return ctx.Err()
	})
	if !gotForce || err == nil {
		t.Fatalf("force/pid not passed or timeout not surfaced: force=%v err=%v", gotForce, err)
	}
}

func TestTaskkillArgs(t *testing.T) {
	if got := taskkillArgs(7, false); len(got) != 3 || got[2] != "/T" {
		t.Fatalf("graceful args = %v", got)
	}
	if got := taskkillArgs(7, true); len(got) != 4 || got[3] != "/F" {
		t.Fatalf("force args = %v", got)
	}
}
