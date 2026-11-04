package officialimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"tallpine.eu/tallpine/backend/internal/process"
)

const (
	// DatabaseFile is the SQLite file name inside an Agent Orchestrator data dir.
	DatabaseFile = "ao.db"

	// copyFallbackLimit bounds the plain-copy fallback used when a
	// copy-on-write clone is unavailable. The official database can be many
	// gigabytes, which is only practical to snapshot as a clone.
	copyFallbackLimit = 256 << 20
	snapshotAttempts  = 3
)

// ErrSourceNotFound reports that the source data dir has no database file.
var ErrSourceNotFound = errors.New("no Agent Orchestrator database found")

// snapshot is a private, disposable copy of the official database.
type snapshot struct {
	dir string
	db  string
}

func (s *snapshot) Close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	return os.RemoveAll(s.dir)
}

// takeSnapshot clones <sourceDataDir>/ao.db (and its -wal) into a fresh
// directory under stagingDir. The only operations performed on the source are
// stat and a read through the clone call, so the live install is never written
// to or locked. The -shm index is not copied: SQLite rebuilds it from the WAL
// when the clone is opened.
//
// A checkpoint of the live database between cloning the main file and its WAL
// could pair mismatched files, so the source's size and mtime are compared
// before and after and the clone is retried when they moved.
func takeSnapshot(ctx context.Context, sourceDataDir, stagingDir string) (*snapshot, error) {
	srcDB := filepath.Join(sourceDataDir, DatabaseFile)
	info, err := os.Stat(srcDB)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w in %s", ErrSourceNotFound, sourceDataDir)
		}
		return nil, fmt.Errorf("stat source database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("source database %s is not a regular file", srcDB)
	}
	if err := os.MkdirAll(stagingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create import staging dir: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt < snapshotAttempts; attempt++ {
		snap, err := cloneOnce(ctx, srcDB, stagingDir)
		if err == nil {
			return snap, nil
		}
		lastErr = err
		if !errors.Is(err, errSourceMoved) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return nil, lastErr
}

var errSourceMoved = errors.New("source database changed while it was being cloned")

func cloneOnce(ctx context.Context, srcDB, stagingDir string) (_ *snapshot, err error) {
	dir, err := os.MkdirTemp(stagingDir, "ao-snapshot-")
	if err != nil {
		return nil, fmt.Errorf("create snapshot dir: %w", err)
	}
	snap := &snapshot{dir: dir, db: filepath.Join(dir, DatabaseFile)}
	defer func() {
		if err != nil {
			_ = snap.Close()
		}
	}()

	before, err := fingerprint(srcDB)
	if err != nil {
		return nil, err
	}
	if err := cloneFile(ctx, srcDB, snap.db); err != nil {
		return nil, err
	}
	if _, statErr := os.Stat(srcDB + "-wal"); statErr == nil {
		if err := cloneFile(ctx, srcDB+"-wal", snap.db+"-wal"); err != nil {
			return nil, err
		}
	}
	after, err := fingerprint(srcDB)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, errSourceMoved
	}
	return snap, nil
}

// fingerprint identifies the main database file's version without reading it.
type fileFingerprint struct {
	size  int64
	mtime int64
}

func fingerprint(path string) (fileFingerprint, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileFingerprint{}, fmt.Errorf("stat source database: %w", err)
	}
	return fileFingerprint{size: info.Size(), mtime: info.ModTime().UnixNano()}, nil
}

// cloneFile makes dst a copy of src. On macOS it asks for a copy-on-write clone
// (cp -c), which is instant and takes no extra space regardless of size. When
// that is unavailable it falls back to a plain copy, but only for small files.
func cloneFile(ctx context.Context, src, dst string) error {
	if runtime.GOOS == "darwin" {
		// #nosec G204 -- fixed binary, arguments are paths, no shell involved.
		err := process.CommandContext(ctx, "/bin/cp", "-c", src, dst).Run()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		_ = os.Remove(dst)
	}
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat %s: %w", filepath.Base(src), err)
	}
	if info.Size() > copyFallbackLimit {
		return fmt.Errorf("cannot snapshot %s (%d MiB): a copy-on-write clone is not available here "+
			"(it needs macOS with the source and %s on the same APFS volume)",
			filepath.Base(src), info.Size()>>20, filepath.Dir(dst))
	}
	return copyFile(src, dst)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(src), err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create snapshot file: %w", err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("copy %s: %w", filepath.Base(src), err)
	}
	return out.Close()
}
