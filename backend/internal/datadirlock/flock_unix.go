//go:build unix

package datadirlock

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// tryLock takes a non-blocking exclusive advisory lock (flock LOCK_EX|LOCK_NB).
// The lock is bound to the open file descriptor, so the kernel drops it when the
// process exits, giving automatic recovery after a crash.
func tryLock(file *os.File) error {
	return lockError(unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB))
}

// lockError maps flock's result: EWOULDBLOCK/EAGAIN means another process holds
// the lock, and ENOTSUP, EOPNOTSUPP or ENOLCK means the filesystem cannot lock.
func lockError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN):
		return ErrLocked
	case errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.ENOLCK):
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}

func unlock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

// inode is the lock file's inode: two daemons only exclude each other when they lock the same inode (B469).
func inode(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino
	}
	return 0
}
