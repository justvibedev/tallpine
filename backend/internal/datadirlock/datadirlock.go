// Package datadirlock guards a daemon's SQLite data directory with an exclusive,
// OS-level advisory file lock.
//
// The lock is scoped to the data directory itself, independent of TALLPINE_PORT and
// TALLPINE_RUN_FILE, so two daemons pointed at the same TALLPINE_DATA_DIR with different
// ports/run files can never both own the database. Without it a second daemon
// can open and hot-migrate SQLite underneath a still-running first daemon,
// invalidating the first daemon's prepared statements against the new schema
// (issue #3716).
//
// Crash recovery is free: the lock lives on the open file descriptor, so the
// kernel releases it automatically when the holder exits — cleanly or by crash,
// SIGKILL included. A successor therefore reclaims a stale lock with no
// PID-liveness bookkeeping. os.OpenFile opens close-on-exec, so tmux servers and
// chat hosts that deliberately outlive the daemon never inherit the lock.
package datadirlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// lockFileName is the advisory-lock file created inside the data directory. It
// carries the holding process's PID as plain text purely for diagnostics; the
// lock itself is enforced by the OS, not by the file's contents.
const lockFileName = "daemon.lock"

// retryInterval paces Acquire while another process holds the lock. Neither
// flock nor LockFileEx can block with a timeout, so a short poll is the
// portable way to bound the wait.
const retryInterval = 100 * time.Millisecond

// ErrLocked is returned by Acquire when another live process still holds the
// data-directory lock after the wait.
var ErrLocked = errors.New("data dir is in use by another Tallpine process")

// ErrUnsupported is returned by Acquire when the data dir's filesystem cannot
// lock files at all (some NFS, SMB and FUSE mounts). Callers start without the
// guard rather than refuse to run, which is how Tallpine behaved before the lock.
var ErrUnsupported = errors.New("file locks are not supported on this filesystem")

// Lock is a held data-directory lock. Release (or the process exiting) frees it.
type Lock struct {
	file *os.File
}

// Acquire takes the exclusive lock for dataDir, creating the directory if
// needed. While another process holds the lock it retries until wait elapses
// (wait <= 0 tries once), then returns an error wrapping ErrLocked that names
// the holder's PID when it can be read.
func Acquire(ctx context.Context, dataDir string, wait time.Duration) (*Lock, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, fmt.Errorf("data dir lock: data dir is required")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("data dir lock: create %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, lockFileName)
	// O_RDWR (not O_TRUNC): the PID line is rewritten only after the lock is
	// held, so a failed acquire leaves the current owner's PID readable.
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("data dir lock: open %s: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	err = tryLock(file)
	for errors.Is(err, ErrLocked) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(retryInterval):
		}
		err = tryLock(file)
	}
	if err != nil {
		holder := readHolderPID(file)
		_ = file.Close()
		if !errors.Is(err, ErrLocked) {
			return nil, fmt.Errorf("data dir lock: %s: %w", path, err)
		}
		if holder > 0 {
			return nil, fmt.Errorf("%w: %s (pid %d)", ErrLocked, dataDir, holder)
		}
		return nil, fmt.Errorf("%w: %s", ErrLocked, dataDir)
	}
	// The lock is ours: record our PID for anyone who later inspects the file.
	if err := writeHolderPID(file, os.Getpid()); err != nil {
		_ = unlock(file)
		_ = file.Close()
		return nil, fmt.Errorf("data dir lock: record owner: %w", err)
	}
	return &Lock{file: file}, nil
}

// Release frees the lock. It is safe to call once; a nil or already-released
// Lock is a no-op. Closing the file descriptor drops the OS lock.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlock(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("data dir lock: release: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("data dir lock: close: %w", closeErr)
	}
	return nil
}

// writeHolderPID records pid for diagnostics. No fsync: readers see the page
// cache, and the lock itself lives in the kernel, not in the file.
func writeHolderPID(file *os.File, pid int) error {
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err := file.WriteAt([]byte(strconv.Itoa(pid)+"\n"), 0)
	return err
}

// stoppingMark starts the lock file's second line once the holder has begun
// its shutdown (MarkStopping), followed by the unix-millisecond time by which
// the holder will have exited. A successor that still finds the lock held well
// after that time may treat the holder as hung and kill it (B564).
const stoppingMark = "stopping"

// MarkStopping records in the lock file that the holder has begun shutting
// down and will have exited by stopBy. It overwrites in place without
// truncating, so a concurrent reader sees either the old or the new text and
// never an empty file. A nil or released Lock is a no-op.
func (l *Lock) MarkStopping(stopBy time.Time) error {
	if l == nil || l.file == nil {
		return nil
	}
	text := fmt.Sprintf("%d\n%s %d\n", os.Getpid(), stoppingMark, stopBy.UnixMilli())
	_, err := l.file.WriteAt([]byte(text), 0)
	return err
}

// Holder is what the lock file says about the process holding the lock: its
// PID (0 when unknown) and, once it has marked itself stopping, the time by
// which it will have exited (zero when not stopping). The file is diagnostic
// text, so callers confirm the lock is still held before acting.
type Holder struct {
	PID    int
	StopBy time.Time
}

// ReadHolder best-effort reads the lock file of dataDir. Any error yields the
// zero Holder, which callers treat as unknown.
func ReadHolder(dataDir string) Holder {
	file, err := os.Open(filepath.Join(dataDir, lockFileName))
	if err != nil {
		return Holder{}
	}
	defer func() { _ = file.Close() }()
	return readHolder(file)
}

// readHolderPID best-effort reads the PID the current owner recorded. Any error
// (empty/partial file during a race) yields 0, which callers treat as unknown.
func readHolderPID(file *os.File) int {
	return readHolder(file).PID
}

// readHolder parses the PID line and the optional stopping mark. A file whose
// first line is not a PID yields the zero Holder.
func readHolder(file *os.File) Holder {
	buf := make([]byte, 64)
	n, err := file.ReadAt(buf, 0)
	if n == 0 && err != nil {
		return Holder{}
	}
	lines := strings.Split(string(buf[:n]), "\n")
	pid, convErr := strconv.Atoi(strings.TrimSpace(lines[0]))
	if convErr != nil || pid <= 0 {
		return Holder{}
	}
	holder := Holder{PID: pid}
	if len(lines) > 1 {
		if mark, at, ok := strings.Cut(strings.TrimSpace(lines[1]), " "); ok && mark == stoppingMark {
			if ms, err := strconv.ParseInt(at, 10, 64); err == nil && ms > 0 {
				holder.StopBy = time.UnixMilli(ms)
			}
		}
	}
	return holder
}

// Describe returns the lock file path and its inode (0 when unknown) for startup diagnostics. Two daemons
// on one data dir only exclude each other while they lock the same inode (B469).
func Describe(dataDir string) (path string, ino uint64) {
	path = filepath.Join(dataDir, lockFileName)
	if info, err := os.Stat(path); err == nil {
		ino = inode(info)
	}
	return path, ino
}
