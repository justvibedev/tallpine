//go:build !windows

package testenv

import (
	"os/exec"
	"syscall"
)

// pidAlive is a raw signal-0 probe rather than processalive.Alive:
// processalive's own tests import testenv, so importing it here would be a
// cycle. A zombie owner reads as alive, which only delays a reap.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) != syscall.ESRCH }

// reapStaleE2EDaemons kills daemons left behind by earlier, interrupted e2e
// runs. Only processes reparented to init (ppid 1) whose executable is
// <tmp>/tallpine-e2e-bin*/tallpine and whose bin dir's owner test process is dead are
// touched, so a live run's daemons and hosts and the real app daemon are
// never reaped. Best effort: errors are ignored.
func reapStaleE2EDaemons() {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output() //nolint:forbidigo // test-only helper; no daemon process package involved.
	if err != nil {
		return
	}
	for _, p := range staleDaemons(string(out), tempRoots()) {
		_ = syscall.Kill(-p.pid, syscall.SIGKILL) // its process group (agent children)
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
}
