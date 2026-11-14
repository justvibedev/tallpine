//go:build !windows

package migratevo

import "syscall"

// migrationSupported is nil on Unix: the vo migration runs.
func migrationSupported() error { return nil }

func sigterm(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

func pidAlive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// sameVolume reports whether a and b are on one filesystem (rename(2) needs it).
func sameVolume(a, b string) (bool, error) {
	var sa, sb syscall.Stat_t
	if err := syscall.Stat(a, &sa); err != nil {
		return false, err
	}
	if err := syscall.Stat(b, &sb); err != nil {
		return false, err
	}
	return sa.Dev == sb.Dev, nil
}
