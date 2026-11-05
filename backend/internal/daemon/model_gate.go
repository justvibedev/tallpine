package daemon

import (
	"context"

	claudecodeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// claudeModelGate marks Claude models the installed Claude Code is too old to
// use. Other agents' catalogs pass through unchanged.
func claudeModelGate(ctx context.Context, agentID string, catalog ports.AgentModelCatalog) ports.AgentModelCatalog {
	if agentID != string(domain.HarnessClaudeCode) {
		return catalog
	}
	binary := catalog.Metadata["binary"]
	if binary == "" {
		resolved, err := claudecodeagent.New().ResolveBinary(ctx)
		if err != nil {
			return catalog
		}
		binary = resolved
	}
	return claudecodeagent.GateModelCatalog(ctx, binary, catalog)
}
