package daemon

import (
	"context"
	"log/slog"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

type claudeAccountLister interface {
	ListAgentAccounts(ctx context.Context) ([]domain.AgentAccount, error)
}

// pruneStalePluginInstalls drops installed_plugins.json entries for Tallpine
// worktrees removed before cleanup pruned them, in the default Claude config
// dir and in every saved Claude account's home. Idempotent and best effort.
func pruneStalePluginInstalls(ctx context.Context, dataDir string, accounts claudeAccountLister, log *slog.Logger) {
	root := filepath.Join(dataDir, "worktrees")
	device, _ := claudeDeviceFolder() // not the inherited CLAUDE_CONFIG_DIR
	dirs := []string{device}
	if list, err := accounts.ListAgentAccounts(ctx); err != nil {
		log.Warn("prune stale plugin installs: list accounts", "err", err)
	} else {
		for _, a := range list {
			if a.Provider == domain.AgentAccountProviderClaude && a.Home != "" {
				dirs = append(dirs, a.Home)
			}
		}
	}
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || seen[filepath.Clean(dir)] {
			continue
		}
		seen[filepath.Clean(dir)] = true
		n, err := skillbridge.PruneStaleWorktreeInstalls(skillbridge.Options{ClaudeConfigDir: dir}, root)
		if err != nil {
			log.Warn("prune stale plugin installs", "dir", dir, "err", err)
		} else if n > 0 {
			log.Info("pruned stale Claude plugin installs of removed worktrees", "dir", dir, "count", n)
		}
	}
}
