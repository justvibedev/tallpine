//go:build !windows

package releaseinstall

import (
	"os"
	"syscall"

	"tallpine.eu/tallpine/backend/internal/process"
)

// startDetached runs argv in a new session with stdin /dev/null and output to
// logPath, so it outlives the daemon that the install itself stops.
func startDetached(argv, env []string, logPath string) (int, error) {
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer func() { _ = logFile.Close() }()
	cmd := process.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go func() { _ = cmd.Wait() }()
	return cmd.Process.Pid, nil
}
