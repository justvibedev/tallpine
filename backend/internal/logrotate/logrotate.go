// Package logrotate is a size-capped, rotating file writer for the daemon log.
// It is deliberately tiny: one active file plus a fixed number of numbered
// backups (daemon.log.1 is the newest), so total disk use stays bounded by
// roughly maxBytes*(keep+1) no matter how long the daemon runs.
package logrotate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// rotateRetryDelay is how long a failed rotation is left alone before the next
// attempt, so a persistent failure (EMFILE, ENOSPC, permissions) does not
// re-run the rename cascade on every log line.
const rotateRetryDelay = time.Second

// Writer appends to path and rotates it once it would exceed maxBytes.
type Writer struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	keep     int
	f        *os.File
	size     int64
	// openFile and now are seams for tests.
	openFile    func(name string, flag int, perm os.FileMode) (*os.File, error)
	now         func() time.Time
	retryRotate time.Time
}

// Open creates the parent directory (0700) and opens path for append (0600).
// keep is the number of rotated backups to retain (minimum 1).
func Open(path string, maxBytes int64, keep int) (*Writer, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("logrotate: maxBytes must be positive")
	}
	if keep < 1 {
		keep = 1
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	w := &Writer{path: path, maxBytes: maxBytes, keep: keep, openFile: os.OpenFile, now: time.Now}
	if err := w.openLocked(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *Writer) openLocked() error {
	f, err := w.openFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f, w.size = f, info.Size()
	return nil
}

// Write implements io.Writer. A single write is never split across files, so a
// log line stays intact; a rotation failure keeps appending to the old file
// rather than dropping the line.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes && !w.now().Before(w.retryRotate) {
		if err := w.rotateLocked(); err != nil {
			w.retryRotate = w.now().Add(rotateRetryDelay)
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotateLocked shifts the backups and opens a fresh active file. The current
// handle is only replaced once the new file is open: if anything fails, w.f is
// left as it was, so later writes keep landing (in the old file, which after a
// successful rename is the newest backup) instead of the log going dead.
func (w *Writer) rotateLocked() error {
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep))
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	// After an earlier failed reopen the active file was already renamed away
	// (the live handle still points at it); there is nothing left to move.
	if err := os.Rename(w.path, w.path+".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	old, oldSize := w.f, w.size
	if err := w.openLocked(); err != nil {
		w.f, w.size = old, oldSize
		return err
	}
	_ = old.Close()
	return nil
}

// Close closes the active file. Later writes return os.ErrClosed.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
