//go:build !windows

package sessionreap

import (
	"errors"
	"syscall"
)

func signalPID(pid int, sig syscall.Signal) error {
	if pid <= 1 {
		return nil
	}
	err := syscall.Kill(pid, sig)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
