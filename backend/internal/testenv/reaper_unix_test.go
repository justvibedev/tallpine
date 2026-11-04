//go:build !windows

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// An orphaned daemon-like process from a tallpine-e2e-bin dir is killed by the
// reaper; reparenting is simulated through a shell that exits immediately.
func TestReapStaleE2EDaemonsKillsOrphan(t *testing.T) {
	skipIfFenced(t)
	dir, err := os.MkdirTemp("", e2eBinPrefix)
	if err != nil {
		t.Fatal(err)
	}
	mkOwner(t, dir, "999999999")
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	fake := filepath.Join(dir, "tallpine")
	if err := os.Symlink(sleep, fake); err != nil {
		t.Fatal(err)
	}
	// The shell backgrounds the fake daemon and exits, so init adopts it.
	out, err := exec.Command("sh", "-c", fake+" 300 >/dev/null 2>&1 & echo $!").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, c := range string(out) {
		if c >= '0' && c <= '9' {
			pid = pid*10 + int(c-'0')
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	time.Sleep(200 * time.Millisecond)
	reapStaleE2EDaemons()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphan pid %d survived reapStaleE2EDaemons", pid)
}
