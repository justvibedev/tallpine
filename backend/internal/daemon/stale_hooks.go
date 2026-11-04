package daemon

import (
	"context"
	"log/slog"

	claudecodeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	codexagent "tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
)

// stripStaleWorkspaceHooks removes Tallpine hook entries older versions wrote into
// existing worktrees' agent settings files. Idempotent and best effort.
func stripStaleWorkspaceHooks(ctx context.Context, worktreesRoot string, log *slog.Logger) {
	claude := claudecodeagent.StripStaleWorkspaceHooks(ctx, worktreesRoot)
	codex := codexagent.StripStaleWorkspaceHooks(ctx, worktreesRoot)
	if claude+codex > 0 {
		log.Info("stripped stale Tallpine hook entries from workspaces", "claude", claude, "codex", codex)
	}
}
