package sessionmanager

import (
	"context"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

// pruneClaudePlugins drops the account's stale project/local plugin installs
// Claude Code recorded for a removed session worktree. Best effort: a failure
// is logged and never blocks cleanup.
func (m *Manager) pruneClaudePlugins(ctx context.Context, rec domain.SessionRecord, workspacePath string) {
	if rec.Harness != domain.HarnessClaudeCode || strings.TrimSpace(workspacePath) == "" {
		return
	}
	env := spawnEnv(rec.ID, rec.ProjectID, rec.IssueID, m.dataDir, nil)
	if project, err := m.loadProject(ctx, rec.ProjectID); err == nil {
		env = m.teardownRuntimeEnv(rec.ID, rec.ProjectID, rec.IssueID, project.Config.Env)
	}
	n, err := skillbridge.PruneWorktreeInstalls(skillbridge.OptionsFor(m.dataDir, workspacePath, env), filepath.Join(m.dataDir, "worktrees"), workspacePath)
	if err != nil {
		m.logger.Warn("workspace cleanup: prune installed plugins failed", "sessionID", rec.ID, "workspacePath", workspacePath, "error", err)
	} else if n > 0 {
		m.logger.Info("workspace cleanup: pruned installed plugin entries", "sessionID", rec.ID, "count", n)
	}
}
