package cleanup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GoBuildCacheMaxAge is how long a Go build cache entry may go unused before
// the rule removes it. Go refreshes an entry's modification time whenever a
// build reads it, so an old time means no build has needed it for that long.
const GoBuildCacheMaxAge = 2 * 24 * time.Hour

// goCacheMarker is the start of the README Go writes into every build cache
// directory. The rule only trims a directory that carries it, so a mistaken
// GOCACHE value can never make it delete unrelated files.
const goCacheMarker = "This directory holds cached build artifacts from the Go build system"

// GoBuildCacheRule trims the machine-wide Go build cache (GOCACHE) that every
// worker session and test run shares. With dozens of worktrees the cache grows
// without bound (119 GB in one incident); Go's own trim only drops entries
// unused for five days. It removes entries unused for GoBuildCacheMaxAge and
// is off by default (policy goBuildCache.enabled) and, while off, only reports
// what it would remove.
type GoBuildCacheRule struct {
	dir    string
	maxAge time.Duration
}

// NewGoBuildCacheRule builds the rule over dir. An empty dir means the
// default location (GOCACHE, else the user cache dir's go-build).
func NewGoBuildCacheRule(dir string) *GoBuildCacheRule {
	if dir == "" {
		dir = defaultGoCacheDir()
	}
	return &GoBuildCacheRule{dir: dir, maxAge: GoBuildCacheMaxAge}
}

func defaultGoCacheDir() string {
	if v := os.Getenv("GOCACHE"); v != "" && v != "off" {
		return v
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "go-build")
}

// Category implements Rule.
func (*GoBuildCacheRule) Category() Category { return CategoryGoBuildCache }

// Light implements Rule: walking the cache belongs to the daily full pass.
func (*GoBuildCacheRule) Light() bool { return false }

// Enabled implements Rule.
func (*GoBuildCacheRule) Enabled(Policy) bool { return true }

// Run implements Rule.
func (r *GoBuildCacheRule) Run(ctx context.Context, run *Run) error {
	if r.dir == "" {
		return nil
	}
	if readme, err := os.ReadFile(filepath.Join(r.dir, "README")); err != nil || !strings.HasPrefix(string(readme), goCacheMarker) {
		return nil //nolint:nilerr // no Go build cache here, nothing to trim.
	}
	dryRun := run.DryRun || !run.Policy.GoBuildCache.Enabled
	item := Item{What: "go build cache entries", Path: r.dir, Status: StatusFreed}
	if dryRun {
		item.Status = StatusWouldFree
	}
	cutoff := run.Now.Add(-r.maxAge)
	// Entries live one level down (xx/<hash>-a, <hash>-d); the README and
	// trim.txt at the top are Go's own bookkeeping.
	subdirs, err := os.ReadDir(r.dir)
	if err != nil {
		return err
	}
	for _, sub := range subdirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !sub.IsDir() {
			continue
		}
		subPath := filepath.Join(r.dir, sub.Name())
		entries, err := os.ReadDir(subPath)
		if err != nil {
			continue // unreadable shard: keep it, try the rest.
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil || !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
				continue
			}
			if !dryRun && os.Remove(filepath.Join(subPath, entry.Name())) != nil {
				continue
			}
			item.Bytes += info.Size()
		}
	}
	if item.Bytes > 0 {
		run.Add(item)
	}
	return nil
}
