// Package sessionartifacts scans a session's artifact directory and
// classifies its durable output type. It only imports domain so both the
// session service (the read path) and the lifecycle reducer (the persist
// path) can depend on it without an import cycle.
package sessionartifacts

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Dir returns the canonical artifact directory for a session under dataDir.
// It is a pure, deterministic path computation (dataDir + "artifacts" + id),
// so callers can derive the correct directory even for a session row whose
// stored ArtifactDir is empty — notably rows created before that column
// existed, which migration 0155 backfilled with ” rather than a real path.
func Dir(dataDir string, id domain.SessionID) string {
	dataDir = strings.TrimSpace(dataDir)
	if dataDir == "" {
		return ""
	}
	return filepath.Join(dataDir, "artifacts", string(id))
}

// VersionsDir is the directory inside a session's artifact directory that
// holds the artifacts' version history. List skips it, so snapshots are not
// artifacts themselves.
const VersionsDir = ".versions"

// MaxFiles bounds how many files List returns so a pathological artifact tree
// cannot stall a session read or the lifecycle reconcile that runs under the
// lifecycle mutex.
const MaxFiles = 1000

// MaxVisited bounds every entry List visits (directories and non-regular
// files included), so a tree of empty directories cannot stall a read.
const MaxVisited = 10000

// List walks a session's artifact directory and returns up to MaxFiles of its
// regular files outside VersionsDir, sorted by path. Entries matched by
// ignored are skipped, as are unreadable entries rather than failing the walk.
// A cancelled ctx stops the walk and returns its error.
func List(ctx context.Context, dir string) ([]domain.SessionArtifactFile, error) {
	files, _, err := walk(ctx, dir)
	return files, err
}

// walk is List that also reports the modification time of every directory it
// listed (keyed by absolute path), which is what ListCached validates against.
// A missing root is (nil, nil, nil).
func walk(ctx context.Context, dir string) ([]domain.SessionArtifactFile, map[string]time.Time, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, nil, nil
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, nil, err
	}
	files := make([]domain.SessionArtifactFile, 0)
	dirs := map[string]time.Time{}
	visited := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		visited++
		if len(files) >= MaxFiles || visited > MaxVisited {
			return filepath.SkipAll
		}
		if d.Type()&os.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && ignored(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if d.Name() == VersionsDir && filepath.Dir(path) == root {
				return filepath.SkipDir
			}
			if info, err := d.Info(); err == nil {
				dirs[path] = info.ModTime()
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil //nolint:nilerr // an entry that cannot be stat'd is skipped, not fatal
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil //nolint:nilerr // an entry outside root is skipped, not fatal
		}
		rel = filepath.ToSlash(rel)
		files = append(files, domain.SessionArtifactFile{
			Path:      rel,
			Name:      filepath.Base(path),
			Kind:      inferKind(rel),
			Size:      info.Size(),
			UpdatedAt: info.ModTime().UTC(),
		})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
	return files, dirs, nil
}

// listCacheTTL bounds how long ListCached serves a listing without walking
// again. Directory modification times catch an entry added, removed or renamed
// anywhere in the listed tree (daemon writes land by rename, so they do too);
// the TTL bounds what they cannot: a file rewritten in place (its Size and
// UpdatedAt), and a change inside the filesystem's mtime granularity.
const listCacheTTL = 5 * time.Second

// listCacheMaxEntries bounds the cache to one listing per recently read
// session directory.
const listCacheMaxEntries = 512

type cachedListing struct {
	files    []domain.SessionArtifactFile
	dirs     map[string]time.Time
	walkedAt time.Time
}

type listCache struct {
	mu      sync.Mutex
	entries map[string]cachedListing
	now     func() time.Time
	walk    func(context.Context, string) ([]domain.SessionArtifactFile, map[string]time.Time, error)
}

var defaultListCache = newListCache()

func newListCache() *listCache {
	return &listCache{entries: map[string]cachedListing{}, now: time.Now, walk: walk}
}

// ListCached is List for hot read paths. The single-session read runs for
// every change event the renderer applies, and walking the directory each time
// cost a readdir per directory and an lstat per file, so it reuses the previous
// walk of dir while every directory it listed still has the same modification
// time and the walk is younger than listCacheTTL. Ignored trees (VersionsDir,
// .git, node_modules, virtualenvs) are never listed, so churn inside them does
// not invalidate the listing. The returned slice is the caller's own copy.
func ListCached(ctx context.Context, dir string) ([]domain.SessionArtifactFile, error) {
	return defaultListCache.list(ctx, dir)
}

func (c *listCache) list(ctx context.Context, dir string) ([]domain.SessionArtifactFile, error) {
	key := strings.TrimSpace(dir)
	if key == "" {
		return nil, nil
	}
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok && c.now().Sub(cached.walkedAt) < listCacheTTL && dirsUnchanged(cached.dirs) {
		return slices.Clone(cached.files), nil
	}
	walkedAt := c.now()
	files, dirs, err := c.walk(ctx, key)
	if err != nil || dirs == nil {
		// A failed walk or a missing root is not cached: either is cheap to
		// find again, and the directory may appear at any moment.
		c.mu.Lock()
		delete(c.entries, key)
		c.mu.Unlock()
		return files, err
	}
	c.mu.Lock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= listCacheMaxEntries {
		c.evictOldestLocked()
	}
	c.entries[key] = cachedListing{files: files, dirs: dirs, walkedAt: walkedAt}
	c.mu.Unlock()
	return slices.Clone(files), nil
}

func (c *listCache) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, entry := range c.entries {
		if oldestKey == "" || entry.walkedAt.Before(oldest) {
			oldestKey, oldest = key, entry.walkedAt
		}
	}
	delete(c.entries, oldestKey)
}

// dirsUnchanged reports whether every directory still has its recorded
// modification time; one that is gone or unreadable counts as changed.
func dirsUnchanged(dirs map[string]time.Time) bool {
	for path, modTime := range dirs {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || !info.ModTime().Equal(modTime) {
			return false
		}
	}
	return true
}

// ignoredNames are OS metadata files and tool directories that List skips,
// matched case-insensitively.
var ignoredNames = map[string]bool{
	".ds_store":       true,
	".spotlight-v100": true,
	".trashes":        true,
	".fseventsd":      true,
	"thumbs.db":       true,
	"desktop.ini":     true,
	".git":            true,
	"node_modules":    true,
	"__pycache__":     true,
	// Python virtual environments agents create inside artifact directories:
	// thousands of files that are not deliverables.
	"venv":  true,
	".venv": true,
}

// regenerableDirs are the tool directories among ignoredNames that hold
// rebuildable output rather than anything a user wrote: dependency trees,
// virtual environments and bytecode caches. Cleanup may remove them from a
// finished session's artifacts without asking. .git is deliberately not one.
var regenerableDirs = map[string]bool{
	"node_modules": true,
	"__pycache__":  true,
	"venv":         true,
	".venv":        true,
}

// RegenerableDir reports whether a directory named name is a rebuildable tool
// directory (see regenerableDirs), matched case-insensitively.
func RegenerableDir(name string) bool { return regenerableDirs[strings.ToLower(name)] }

// ignored reports whether List skips an entry named name: ignoredNames and
// macOS "._" resource-fork files.
func ignored(name string) bool {
	return strings.HasPrefix(name, "._") || ignoredNames[strings.ToLower(name)]
}

// IgnoredPath reports whether any segment of the slash-separated relative path
// is one List skips, so version rows recorded for such a path before the skip
// existed can be recognized as not deliverables.
func IgnoredPath(rel string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
		if seg != "" && ignored(seg) {
			return true
		}
	}
	return false
}

func inferKind(relPath string) domain.SessionArtifactKind {
	switch strings.ToLower(filepath.Ext(relPath)) {
	case ".html", ".htm":
		return domain.SessionArtifactHTML
	case ".md", ".markdown":
		return domain.SessionArtifactMarkdown
	}
	return domain.SessionArtifactGeneric
}

// DeriveOutputType classifies a session's durable output from counts alone.
// PR and artifact are independent facts that combine into PRAndArtifact when
// both are present. Once a PR row exists for a session it is never deleted
// (merge/close does not remove it), so prCount only grows; combined with
// artifact files only ever being added, never removed from this
// classification's perspective, the result never reverts (none -> artifact
// and/or pr -> pr_artifact).
func DeriveOutputType(prCount, artifactFileCount int) domain.SessionOutputType {
	hasPR := prCount > 0
	hasArtifact := artifactFileCount > 0
	switch {
	case hasPR && hasArtifact:
		return domain.SessionOutputPRAndArtifact
	case hasPR:
		return domain.SessionOutputPR
	case hasArtifact:
		return domain.SessionOutputArtifact
	default:
		return domain.SessionOutputNone
	}
}
