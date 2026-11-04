package codex

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// findRollout is the one Codex rollout lookup behind Exists, Probe and Locate.
// An account home links its sessions dir to the computer's own and WalkDir
// does not descend into a root that is itself a link, so the root is resolved
// first. Names match rollout-*-<id>.jsonl, optionally .zst-compressed.
// requireNonEmpty rejects zero-byte files (a reserved thread with no rollout).
func findRollout(ctx context.Context, root, id string, requireNonEmpty bool) (string, bool, error) {
	walkRoot := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		walkRoot = resolved
	}
	var found string
	err := filepath.WalkDir(walkRoot, func(path string, entry fs.DirEntry, walkErr error) error { // #nosec G703 -- read-only walk of the codex home under the user's own home; no agent-supplied path
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || !codexRolloutNameMatches(entry.Name(), id) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() && (!requireNonEmpty || info.Size() > 0) {
			// Report the path under the caller's root, not the resolved one.
			if rel, relErr := filepath.Rel(walkRoot, path); relErr == nil {
				path = filepath.Join(root, rel)
			}
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("codex: inspect rollout root %s: %w", root, err)
	}
	return found, found != "", nil
}

func codexRolloutNameMatches(name, id string) bool {
	if !strings.HasPrefix(name, "rollout-") {
		return false
	}
	suffix := "-" + id + ".jsonl"
	return strings.HasSuffix(name, suffix) || strings.HasSuffix(name, suffix+".zst")
}

const codexHomeEnv = "CODEX_HOME"
