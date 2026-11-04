package contextwindow

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ClaudeAutoCompactWindowEnv is the env var holding the token count at which
// Claude Code compacts on its own. Claude Code then reports that count as its
// window, which is why a report is never read back as capacity.
const ClaudeAutoCompactWindowEnv = "CLAUDE_CODE_AUTO_COMPACT_WINDOW"

// CodexAutoCompactLimitEnv carries the compaction token limit to the Codex
// chat driver, which puts it in the thread config as
// model_auto_compact_token_limit (Codex has no env var of its own).
const CodexAutoCompactLimitEnv = "TALLPINE_CODEX_AUTO_COMPACT_LIMIT"

// AutoCompactEnvKey is the launch-env key a harness's chat reads its compaction
// window from; "" means the harness has no compaction-window capability and
// is left unchanged.
func AutoCompactEnvKey(harness domain.AgentHarness) string {
	switch harness {
	case domain.HarnessClaudeCode:
		return ClaudeAutoCompactWindowEnv
	case domain.HarnessCodex:
		return CodexAutoCompactLimitEnv
	}
	return ""
}

// CatalogReader reads the saved model catalog for a harness and project.
type CatalogReader interface {
	Models(ctx context.Context, agentID, scope string, refresh bool) (ports.AgentModelCatalog, error)
}

// Lookup is the model's capacity and display name from the saved catalog (see
// Capacity and Label). With no reader or a failed read it still resolves
// against an empty catalog, so family aliases such as "opus" keep their static
// window and label.
func Lookup(ctx context.Context, reader CatalogReader, harness, project, model string) (int64, string) {
	var catalog ports.AgentModelCatalog
	if reader != nil {
		if saved, err := reader.Models(ctx, harness, project, false); err == nil {
			catalog = saved
		}
	}
	return Capacity(catalog, model), Label(catalog, model)
}
