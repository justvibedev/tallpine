package artifactversions

import (
	"context"
	"errors"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// TrimAll applies the version cap to history recorded under an older, larger
// cap, and drops the history of paths that are no longer versioned (virtual
// environments, node_modules, .git, caches). It visits every session that has
// version rows, terminated ones included, pausing between sessions so the
// capture loop and chat writes are not starved. Blobs left unreferenced are
// removed. It stops when ctx is done.
func (c *Capturer) TrimAll(ctx context.Context, pause time.Duration) error {
	ids, err := c.store.ListSessionIDsWithArtifactVersions(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.trimSession(ctx, id); err != nil {
			c.logger.Debug("artifact versions: trim failed", "session", id, "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pause):
		}
	}
	return nil
}

func (c *Capturer) trimSession(ctx context.Context, id domain.SessionID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	paths, err := c.store.ListSessionArtifactVersionPaths(ctx, id)
	if err != nil {
		return err
	}
	var sums []string
	for _, p := range paths {
		if !sessionartifacts.IgnoredPath(p) {
			continue
		}
		removed, err := c.store.DeleteSessionArtifactPathVersions(ctx, id, p)
		if err != nil {
			return err
		}
		sums = append(sums, removed...)
	}
	trimmed, err := c.store.TrimSessionArtifactVersions(ctx, id, c.keep)
	if err != nil {
		return err
	}
	sums = append(sums, trimmed...)
	if len(sums) == 0 {
		return nil
	}
	root, err := os.OpenRoot(sessionartifacts.ResolveForID(ctx, c.dataDir, c.store, id, false))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer func() { _ = root.Close() }()
	blobs := blobStore{root: root}
	seen := make(map[string]bool, len(sums))
	for _, sum := range sums {
		if !seen[sum] {
			seen[sum] = true
			c.removeUnreferenced(ctx, id, blobs, sum)
		}
	}
	return nil
}
