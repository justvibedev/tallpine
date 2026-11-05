package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestResolveClaudeModelRefusesModelInstalledCLIIsTooOldFor(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "claude-opus-5-5"},
	}
	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		Models: []ports.AgentModelInfo{{
			ID: "claude-opus-5-5", RequiresVersion: "2.1.280", UnavailableReason: "Requires Claude Code 2.1.280 or newer. Run `claude update`, then try again.",
		}},
	}}}
	_, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{})
	if !errors.Is(err, ErrUnsupportedModel) || !strings.Contains(err.Error(), "claude update") {
		t.Fatalf("error = %v, want ErrUnsupportedModel carrying the remedy", err)
	}
}
