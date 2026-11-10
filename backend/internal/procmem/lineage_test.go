//go:build darwin || linux

package procmem

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"testing"
)

func TestParentChainWalksFromSelf(t *testing.T) {
	chain, err := ParentChain(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) < 2 || chain[0] != os.Getpid() || chain[1] != os.Getppid() {
		t.Fatalf("chain = %v (self %d parent %d)", chain, os.Getpid(), os.Getppid())
	}
	if slices.Contains(chain, 1) {
		t.Fatalf("chain must stop before pid 1: %v", chain)
	}
}

func TestExitedProcessIsGone(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0") //nolint:forbidigo // real exited pid
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if _, err := ParentChain(pid); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("ParentChain = %v", err)
	}
	if _, err := SessionOf(pid); !errors.Is(err, ErrProcessGone) {
		t.Fatalf("SessionOf = %v", err)
	}
}

func TestZombieIsGone(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0") //nolint:forbidigo // real zombie while unreaped
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	for range 200 {
		if _, err := ParentChain(pid); errors.Is(err, ErrProcessGone) {
			return
		}
		waitBriefly()
	}
	t.Fatalf("unreaped exited child never read as gone")
}

func TestSessionOfMatchesGetsid(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30") //nolint:forbidigo // real child in our session
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	got, err := SessionOf(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	self, err := SessionOf(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if got != self || got <= 0 {
		t.Fatalf("child session %d, own session %d", got, self)
	}
}

func TestExecutablePathOfSelf(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if got := ExecutablePath(os.Getpid()); got != self {
		t.Fatalf("ExecutablePath = %q, want %q", got, self)
	}
	if got := ExecutablePath(1 << 22); got != "" {
		t.Fatalf("missing pid = %q", got)
	}
}

func TestChildPIDsListsADirectChild(t *testing.T) {
	cmd := exec.Command("/bin/sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	children, err := ChildPIDs(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(children, cmd.Process.Pid) {
		t.Fatalf("children %v do not include %d", children, cmd.Process.Pid)
	}
}
