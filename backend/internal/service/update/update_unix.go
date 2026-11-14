//go:build !windows

package update

import (
	"os"
	"syscall"

	"tallpine.eu/tallpine/backend/internal/process"
)

// pidAlive reports whether pid names a live process.
func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// launchDetached runs the job in its own session, so quitting the daemon or
// the app does not signal it.
func launchDetached(script string, env []string, logPath string) (int, error) {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = logf.Close() }()
	cmd := process.Command("/bin/bash", script)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid, nil
}
