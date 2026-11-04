package claudeacp

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
)

// claudeAuthRejected scopes the refusal to the refused launch's credential.
// Resolving it runs `claude auth status`, so it leaves the turn path; the
// daemon is told only after the refusal is recorded, so its catalog re-read
// already sees the signed-out login.
func claudeAuthRejected(plugin claudePlugin, notifyDaemon func()) func(acpdriver.LaunchConfig) {
	return func(cfg acpdriver.LaunchConfig) {
		go func() {
			ctx := context.Background()
			if binary, err := plugin.ResolveBinary(ctx); err == nil {
				claudecode.NoteProviderRefusedLogin(ctx, binary, cfg.WorkspacePath, cfg.Env)
			} else {
				claudecode.InvalidateAuthCache()
			}
			if notifyDaemon != nil {
				notifyDaemon()
			}
		}()
	}
}
