package codex

import (
	"context"
	"log/slog"
	"path/filepath"
)

// StripStaleWorkspaceHooks removes Tallpine-owned Codex hook entries from every
// <worktreesRoot>/<project>/<session>/.codex/hooks.json (hooks now ride -c
// launch flags). Foreign hooks are preserved; clean files are not rewritten.
// It returns the number of files changed.
func StripStaleWorkspaceHooks(ctx context.Context, worktreesRoot string) int {
	// <project>/<session> and <project>/orchestrator/<name> layouts.
	matches, _ := filepath.Glob(filepath.Join(worktreesRoot, "*", "*", codexHooksDirName, codexHooksFileName))
	deeper, _ := filepath.Glob(filepath.Join(worktreesRoot, "*", "*", "*", codexHooksDirName, codexHooksFileName))
	matches = append(matches, deeper...)
	changed := 0
	for _, hooks := range matches {
		if ctx.Err() != nil {
			break
		}
		workspace := filepath.Dir(filepath.Dir(hooks))
		installed, err := (&Plugin{}).AreHooksInstalled(ctx, workspace)
		if err != nil {
			slog.Warn("codex: skipped stale hook cleanup", "workspace", workspace, "error", err)
			continue
		}
		if !installed {
			continue
		}
		if err := removeLegacyWorkspaceHooks(workspace); err != nil {
			slog.Warn("codex: stale hook cleanup failed", "workspace", workspace, "error", err)
			continue
		}
		changed++
	}
	return changed
}
