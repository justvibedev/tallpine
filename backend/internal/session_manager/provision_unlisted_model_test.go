package sessionmanager

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestResolveClaudeUnlistedModelPassesThroughOnRenewableLoginWithoutAccountCatalog(t *testing.T) {
	t.Parallel()

	cfg := ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		RequestedMode: domain.SessionModeTUI, AgentConfig: ports.AgentConfig{Model: "provider/new", Effort: "high"},
	}
	fallback := ports.AgentModelCatalog{
		Models: []ports.AgentModelInfo{{ID: "provider/old"}}, Stale: true, RefreshState: "error",
		WarningCode: ports.ModelCatalogWarningAuthExpired,
	}
	var refreshes []bool
	m := &Manager{modelCatalog: refreshRecordingCatalog{refreshes: &refreshes, saved: fallback, fresh: fallback}}
	got, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{})
	if err != nil || got.Model != "provider/new" {
		t.Fatalf("resolution = %+v, %v; want unlisted model passed to the CLI", got, err)
	}
	cfg.AgentConfig.Effort = "bogus"
	if _, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}); !errors.Is(err, ports.ErrUnsupportedEffort) {
		t.Fatalf("bogus effort error = %v, want ErrUnsupportedEffort", err)
	}
	last := time.Now()
	fallback.LastSuccessAt = &last
	m.modelCatalog = refreshRecordingCatalog{refreshes: &refreshes, saved: fallback, fresh: fallback}
	cfg.AgentConfig.Effort = "high"
	if _, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, cfg, domain.ProjectConfig{}); !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("account-scoped catalog error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}
