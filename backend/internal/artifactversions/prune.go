package artifactversions

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// ErrPruneRefused reports that the session is no longer eligible for a prune
// when re-read: it is live again, unknown, ended too recently, or keeps its
// artifacts outside the canonical directory. Nothing was removed.
var ErrPruneRefused = errors.New("prune refused")

// PruneRegenerable deletes a session's version history (its rows and blobs)
// and the rebuildable tool directories in its artifact directory
// (node_modules, virtual environments, __pycache__), and keeps every
// deliverable file. It returns the bytes removed.
//
// The caller decided from a snapshot that may be minutes old, so the session
// is re-read here, under the capture lock, right before anything goes: it must
// still be terminated, not a task preparation, ended before endedBefore, and
// keep its artifacts in the canonical directory. Otherwise the prune is
// refused with ErrPruneRefused and nothing is removed.
//
// It holds the capture lock, so no capture records a version between the rows
// going and the blobs going. Rows go first: a blob left behind by a failed
// removal is unreferenced and goes with the next prune, while a row whose blob
// is gone would offer a restore that cannot work. Every removal goes through
// an os.Root at the artifact directory, so nothing outside it can be reached.
func (c *Capturer) PruneRegenerable(ctx context.Context, id domain.SessionID, endedBefore time.Time) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if sessionartifacts.Dir(c.dataDir, id) == "" {
		return 0, errors.New("prune artifacts: no data directory")
	}
	rec, found, err := c.store.GetSession(ctx, id)
	if err != nil {
		return 0, err
	}
	dir := sessionartifacts.ResolveManaged(c.dataDir, id, rec.Metadata.ArtifactDir)
	switch {
	case !found:
		return 0, fmt.Errorf("%w: session not found", ErrPruneRefused)
	case !rec.IsTerminated || rec.IsTaskPreparation:
		return 0, fmt.Errorf("%w: session is live", ErrPruneRefused)
	case !rec.UpdatedAt.Before(endedBefore):
		return 0, fmt.Errorf("%w: session ended too recently", ErrPruneRefused)
	case !sessionartifacts.IsManaged(c.dataDir, id, rec.Metadata.ArtifactDir):
		return 0, fmt.Errorf("%w: artifacts are outside the managed artifact directory", ErrPruneRefused)
	}
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()

	var targets []string
	if info, err := root.Lstat(sessionartifacts.VersionsDir); err == nil && info.IsDir() {
		targets = append(targets, sessionartifacts.VersionsDir)
	}
	walkErr := fs.WalkDir(root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil || path == "." {
			return nil //nolint:nilerr // an unreadable entry is skipped, not fatal
		}
		if !d.IsDir() {
			return nil
		}
		if path == sessionartifacts.VersionsDir {
			return fs.SkipDir
		}
		if sessionartifacts.RegenerableDir(d.Name()) {
			targets = append(targets, path)
			return fs.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	if err := c.store.DeleteSessionArtifactVersions(ctx, id); err != nil {
		return 0, err
	}
	delete(c.observed, id)
	delete(c.swept, id)

	var freed int64
	var errs []error
	for _, rel := range targets {
		size := treeSize(root, rel)
		if err := root.RemoveAll(filepath.FromSlash(rel)); err != nil {
			errs = append(errs, err)
			continue
		}
		freed += size
	}
	return freed, errors.Join(errs...)
}

// treeSize sums the regular files under rel inside root.
func treeSize(root *os.Root, rel string) int64 {
	var size int64
	_ = fs.WalkDir(root.FS(), rel, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil //nolint:nilerr // unreadable entries do not count
		}
		if info, err := d.Info(); err == nil {
			size += info.Size()
		}
		return nil
	})
	return size
}
