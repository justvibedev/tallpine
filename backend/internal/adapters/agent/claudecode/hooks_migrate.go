package claudecode

import (
	"context"
	"log/slog"
	"path/filepath"
)

// StripStaleWorkspaceHooks removes Tallpine's `tallpine hooks claude-code` / legacy
// `vo hooks claude-code` entries from every <worktreesRoot>/<project>/<session>
// .claude/settings.local.json. Hooks ride the --settings launch flag now, so
// these leftovers only fire when the owner runs a plain `claude` there, where
// the CLI is not on PATH ("vo: command not found"). Foreign hooks and unrelated
// settings are preserved; files without Tallpine entries are not rewritten, so it is
// idempotent. It returns the number of files changed.
func StripStaleWorkspaceHooks(ctx context.Context, worktreesRoot string) int {
	// <project>/<session> and <project>/orchestrator/<name> layouts.
	matches, _ := filepath.Glob(filepath.Join(worktreesRoot, "*", "*", claudeSettingsDirName, claudeSettingsFileName))
	deeper, _ := filepath.Glob(filepath.Join(worktreesRoot, "*", "*", "*", claudeSettingsDirName, claudeSettingsFileName))
	matches = append(matches, deeper...)
	changed := 0
	for _, settings := range matches {
		if ctx.Err() != nil {
			break
		}
		workspace := filepath.Dir(filepath.Dir(settings))
		installed, err := claudeHooks.AreInstalled(ctx, workspace)
		if err != nil || !installed {
			if err != nil {
				slog.Warn("claude-code: skipped stale hook cleanup", "workspace", workspace, "error", err)
			}
			continue
		}
		if err := claudeHooks.Uninstall(ctx, workspace); err != nil {
			slog.Warn("claude-code: stale hook cleanup failed", "workspace", workspace, "error", err)
			continue
		}
		changed++
	}
	return changed
}
