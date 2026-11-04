//go:build !windows

package acp

import (
	"os/exec"
	"syscall"
	"time"

	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree asks the adapter's process group to exit before it kills
// it, so the agent can stop the commands it runs in groups of their own.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return tallpineprocess.TerminateGroup(cmd.Process.Pid, 2*time.Second)
}
