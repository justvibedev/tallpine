package daemon

import (
	"context"
	"fmt"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/nativeconfig"
	usagesvc "tallpine.eu/tallpine/backend/internal/service/usage"
)

// usageSourceRoots resolves the provider transcript directories the usage
// collector may read, from the daemon's own environment. service/usage takes
// them injected and never reads process env itself.
func usageSourceRoots(ctx context.Context) (usagesvc.SourceRoots, error) {
	if err := ctx.Err(); err != nil {
		return usagesvc.SourceRoots{}, err
	}
	codexHome, err := nativeconfig.CodexHome(nil)
	if err != nil {
		return usagesvc.SourceRoots{}, fmt.Errorf("resolve codex home: %w", err)
	}
	claudeHome, _, err := nativeconfig.ClaudeConfigDir(nil)
	if err != nil {
		return usagesvc.SourceRoots{}, fmt.Errorf("resolve claude config dir: %w", err)
	}
	return usagesvc.SourceRoots{
		ClaudeProjects: filepath.Join(claudeHome, "projects"),
		CodexSessions:  filepath.Join(codexHome, "sessions"),
		CodexArchived:  filepath.Join(codexHome, "archived_sessions"),
	}, nil
}
