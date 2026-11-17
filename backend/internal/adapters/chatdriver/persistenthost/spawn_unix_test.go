//go:build !windows

package persistenthost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDetachedChatHostsAreReapedAfterRepeatedShutdown(t *testing.T) {
	cfg := Config{
		SessionID: "reaped-host", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	for range 3 {
		transport, err := ConnectOrStart(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		_ = transport.Stdin.Close()
		if err := Shutdown(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err := syscall.Kill(d.PID, 0); errors.Is(err, syscall.ESRCH) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("detached chat host %d remained a child after shutdown", d.PID)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestShutdownQuickSignalsSooner(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quick    bool
		min, max time.Duration
	}{
		{name: "quick stop", quick: true, min: providerQuickGrace, max: providerStdinGrace},
		{name: "full grace", quick: false, min: providerStdinGrace, max: providerStopDeadline},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{
				SessionID: "stdin-ignorer", DataDir: t.TempDir(), Workdir: t.TempDir(),
				Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1", "TALLPINE_CHAT_HOST_IGNORE_STDIN=1"),
				Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
			}
			t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
			transport, err := ConnectOrStart(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			_ = transport.Stdin.Close()
			started := time.Now()
			stop := Shutdown
			if tc.quick {
				stop = ShutdownQuick
			}
			if err := stop(context.Background(), cfg.DataDir, cfg.SessionID); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(started); took < tc.min || took >= tc.max {
				t.Fatalf("shutdown took %s, want within [%s, %s)", took, tc.min, tc.max)
			}
		})
	}
}

// A host stopped by a signal (launchd sends SIGTERM to every process at
// logout) takes its provider with it instead of leaving it running unowned.
func TestSignalledHostStopsItsProvider(t *testing.T) {
	cfg := Config{
		SessionID: "signalled-host", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Stdin.Close() }()
	d, err := readDescriptor(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	provider := 0
	deadline := time.Now().Add(5 * time.Second)
	for provider == 0 {
		out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=").Output()
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			var pid, ppid int
			if n, _ := fmt.Sscan(line, &pid, &ppid); n == 2 && ppid == d.PID {
				provider = pid
			}
		}
		if provider == 0 {
			if time.Now().After(deadline) {
				t.Fatal("provider process not found under the host")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	if err := syscall.Kill(d.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for {
		hostGone := errors.Is(syscall.Kill(d.PID, 0), syscall.ESRCH)
		providerGone := errors.Is(syscall.Kill(provider, 0), syscall.ESRCH)
		if hostGone && providerGone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after SIGTERM: host gone=%v provider gone=%v", hostGone, providerGone)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "chat-hosts", cfg.SessionID, "host.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descriptor after signalled exit: %v", err)
	}
}

// A host that is slow to launch (a loaded machine) must still be attached once
// it publishes, even past the quiet startupTimeout.
func TestConnectOrStartWaitsForSlowLaunchingHost(t *testing.T) {
	old := startupTimeout
	startupTimeout = 200 * time.Millisecond
	t.Cleanup(func() { startupTimeout = old })
	cfg := Config{
		SessionID: "slow-host", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1", "TALLPINE_CHAT_HOST_TEST_DELAY=1500ms"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), cfg.DataDir, cfg.SessionID) })
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatalf("slow host start failed: %v", err)
	}
	_ = transport.Stdin.Close()
}

// A host that dies before publishing fails the start at once instead of
// burning the whole wait.
func TestConnectOrStartFailsFastWhenHostExits(t *testing.T) {
	cfg := Config{
		SessionID: "dead-host", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_TEST_EXIT=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	begin := time.Now()
	if _, err := ConnectOrStart(context.Background(), cfg); err == nil {
		t.Fatal("start succeeded although the host exited")
	}
	// Bounded well below startupMaxWait (a reverted loop waits the full 2 min);
	// the bound includes exec time, so it must not be a tight budget.
	if took := time.Since(begin); took > startupMaxWait/2 {
		t.Fatalf("start took %s after the host exited", took)
	}
}

// Two starters race: ours exits (lost the lock) while the winner publishes a
// moment later. Our start must still attach to the winner.
func TestConnectOrStartAttachesToRacingHostAfterOwnExit(t *testing.T) {
	dataDir, workdir := t.TempDir(), t.TempDir()
	base := Config{
		SessionID: "race-host", DataDir: dataDir, Workdir: workdir,
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, base.SessionID) })
	winner := base
	winner.Env = append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1", "TALLPINE_CHAT_HOST_TEST_DELAY=300ms")
	cmd := exec.Command(os.Args[0], hostArgs(winner)...) //nolint:gosec // test re-exec of itself as a chat host
	cmd.Env = winner.Env
	cmd.Dir = workdir
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	loser := base
	loser.Env = append(os.Environ(), "TALLPINE_CHAT_HOST_TEST_EXIT=1")
	transport, err := ConnectOrStart(context.Background(), loser)
	if err != nil {
		t.Fatalf("start did not attach to the racing host: %v", err)
	}
	_ = transport.Stdin.Close()
}

// While a live process holds the launch lock (a racing host still starting),
// a start whose own host exited keeps waiting past hostExitGrace.
func TestConnectOrStartWaitsWhileLaunchLockHeld(t *testing.T) {
	old := hostExitGrace
	hostExitGrace = 50 * time.Millisecond
	t.Cleanup(func() { hostExitGrace = old })
	cfg := Config{
		SessionID: "lock-held", DataDir: t.TempDir(), Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_TEST_EXIT=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	lock, err := lockPath(cfg.DataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(lock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lock, fmt.Appendf(nil, "%d\n", os.Getpid()), 0o600); err != nil {
		t.Fatal(err)
	}
	const held = 1500 * time.Millisecond
	time.AfterFunc(held, func() { _ = os.Remove(lock) })
	begin := time.Now()
	if _, err := ConnectOrStart(context.Background(), cfg); err == nil {
		t.Fatal("start succeeded without any host")
	}
	if took := time.Since(begin); took < held-100*time.Millisecond {
		t.Fatalf("start gave up after %s while a live process held the launch lock", took)
	}
}
