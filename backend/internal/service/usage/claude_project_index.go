package usage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// claudeIndexRacyWindow keeps a directory uncacheable until its mtime is safely
// older than the listing that saw it. A file created in the same mtime tick as
// a listing leaves the mtime unchanged, so that listing cannot be trusted; two
// seconds covers the coarsest timestamp granularity a home directory can have.
const claudeIndexRacyWindow = 2 * time.Second

// claudeProjectIndex answers "which project directories hold this transcript"
// without reopening every directory for every lookup.
//
// ~/.claude/projects gains a directory per worktree and is never pruned, so it
// reaches thousands of entries. Globbing root/*/<id>.jsonl costs a stat, open,
// getdirentries and close per directory per binding, and a discovery pass runs
// for up to defaultDiscoveryLimit bindings: on a real install that was the
// daemon's dominant CPU cost. Here a refresh stats each directory once and
// re-reads only those whose mtime moved, which is the only way a transcript can
// appear or disappear in them.
type claudeProjectIndex struct {
	root      string
	statDir   func(string) (os.FileInfo, error)
	readNames func(string) ([]string, error)

	mu     sync.Mutex
	dirs   map[string]claudeProjectDir
	byFile map[string][]string
}

type claudeProjectDir struct {
	modTime     time.Time
	listedAt    time.Time
	transcripts []string
}

func newClaudeProjectIndex(root string) *claudeProjectIndex {
	return &claudeProjectIndex{root: root, statDir: os.Stat, readNames: readDirNames}
}

func readDirNames(dir string) ([]string, error) {
	file, err := os.Open(dir) //nolint:gosec // dir is a child of the configured provider root.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return file.Readdirnames(-1)
}

// reusable reports whether a listing can stand in for a fresh read of a
// directory whose current mtime is modTime.
func (d claudeProjectDir) reusable(modTime time.Time) bool {
	return d.modTime.Equal(modTime) && d.listedAt.Sub(d.modTime) > claudeIndexRacyWindow
}

// refresh brings the index up to date with the filesystem. Like filepath.Glob,
// which it replaces, it treats unreadable directories as empty; only context
// cancellation is an error.
func (x *claudeProjectIndex) refresh(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.root == "" {
		return nil
	}
	entries, _ := os.ReadDir(x.root)
	dirs := make(map[string]claudeProjectDir, len(entries))
	byFile := make(map[string][]string, len(x.byFile))
	// os.ReadDir sorts by name, so each byFile slice stays in the path order a
	// glob would have produced.
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Join(x.root, entry.Name())
		// Stat, not the entry's own type: a symlinked project directory counts.
		info, err := x.statDir(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		listing, cached := x.dirs[dir]
		if !cached || !listing.reusable(info.ModTime()) {
			listing = x.list(dir, info.ModTime())
		}
		dirs[dir] = listing
		for _, name := range listing.transcripts {
			byFile[name] = append(byFile[name], filepath.Join(dir, name))
		}
	}
	x.dirs, x.byFile = dirs, byFile
	return nil
}

func (x *claudeProjectIndex) list(dir string, modTime time.Time) claudeProjectDir {
	// Taken before the read so a file created during it counts as racy.
	listedAt := time.Now()
	names, err := x.readNames(dir)
	if err != nil {
		// A failed read must not look like an empty directory next time.
		listedAt = time.Time{}
	}
	transcripts := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasSuffix(name, ".jsonl") {
			transcripts = append(transcripts, name)
		}
	}
	return claudeProjectDir{modTime: modTime, listedAt: listedAt, transcripts: transcripts}
}

// lookup returns every path of the named transcript as of the last refresh.
func (x *claudeProjectIndex) lookup(file string) []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]string(nil), x.byFile[file]...)
}
