//go:build !windows

package process

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessTreeContext(_ context.Context, cmd *exec.Cmd, _ time.Duration) error {
	return killProcessTree(cmd)
}

// KillPIDTree signals pid's process group: SIGKILL when force, else SIGTERM.
func KillPIDTree(pid int, force bool) error {
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// TerminateGroup asks process group pgid to exit with SIGTERM and kills what
// is left after grace. A program that cleans up on SIGTERM gets to: Claude
// Code stops the commands it runs in process groups of their own, which a bare
// SIGKILL of its group would leave running. Reap the group's leader as it
// exits, or its zombie keeps the group alive until the grace runs out.
func TerminateGroup(pgid int, grace time.Duration) error {
	if err := syscall.Kill(-pgid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		return nil
	}
	deadline := time.Now().Add(grace)
	for groupAlive(pgid) {
		if time.Now().After(deadline) {
			err := syscall.Kill(-pgid, syscall.SIGKILL)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

// groupAlive reports whether pgid has members. EPERM counts: macOS returns it
// for a group whose only members are zombies.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
