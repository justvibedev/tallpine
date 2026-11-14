package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestResolveBuiltinDefaultModelFallsBackToAgentDefault(t *testing.T) {
	t.Parallel()
	stale := ports.AgentModelCatalog{Models: []ports.AgentModelInfo{{ID: "other"}}, Stale: true, RefreshState: "error"}
	var refreshes []bool
	m := &Manager{logger: slog.New(slog.DiscardHandler), modelCatalog: refreshRecordingCatalog{refreshes: &refreshes, saved: stale, fresh: stale}}
	cfg := ports.SpawnConfig{ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, RequestedMode: domain.SessionModeTUI}
	resolve := func(c ports.SpawnConfig, p domain.ProjectConfig) (ports.AgentConfig, error) {
		return m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, c, p)
	}
	builtin := domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode, AgentConfig: domain.AgentConfig{Model: domain.BuiltinWorkerModel, Effort: "medium"}}}
	got, err := resolve(cfg, builtin)
	if err != nil || got.Model != "" || got.Effort != "" {
		t.Fatalf("built-in default = %+v, %v; want the agent's own default model", got, err)
	}
	// An owner-set model the catalog cannot confirm keeps failing.
	custom := builtin
	custom.Worker.AgentConfig.Model = "claude-custom-9"
	if _, err := resolve(cfg, custom); !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("custom model error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
	// So does a model carried by the spawn request itself.
	cfg.AgentConfig.Model = domain.BuiltinWorkerModel
	if _, err := resolve(cfg, builtin); !errors.Is(err, ports.ErrModelCapabilitiesUnavailable) {
		t.Fatalf("requested model error = %v, want ErrModelCapabilitiesUnavailable", err)
	}
}
