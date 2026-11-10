package sessionmanager

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B635: the account's model list can lag a release Claude Code already serves.
func TestResolveClaudeModelAcceptsWellFormedIDMissingFromCatalog(t *testing.T) {
	t.Parallel()

	m := &Manager{modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
		CustomModelEntry: ports.CustomModelEntryDirect,
		Models:           []ports.AgentModelInfo{{ID: "claude-fable-5-1", Efforts: []string{"high"}}},
	}}}
	spawn := func(model string) (ports.AgentConfig, error) {
		return m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
			ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			AgentConfig: ports.AgentConfig{Model: model},
		}, domain.ProjectConfig{})
	}
	got, err := spawn("claude-fable-5-2")
	if err != nil || got.Model != "claude-fable-5-2" {
		t.Fatalf("claude-fable-5-2 = %+v, %v; want accepted", got, err)
	}
	// An effort inherited from the project default was chosen for another model.
	inherited, err := m.resolveAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "claude-haiku-5-9"},
	}, domain.ProjectConfig{Worker: domain.RoleOverride{AgentConfig: domain.AgentConfig{Model: "claude-fable-5-1", Effort: "max"}}})
	if err != nil || inherited.Effort != "" {
		t.Fatalf("inherited effort = %q, %v; want cleared", inherited.Effort, err)
	}
	// Chat silently keeps Claude Code's model for an ID its session does not offer.
	_, err = m.resolveChatAgentConfig(context.Background(), agentpolicy.Decision{}, ports.SpawnConfig{
		ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AgentConfig: ports.AgentConfig{Model: "claude-fable-5-2"},
	}, domain.ProjectConfig{})
	if !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("chat unlisted ID error = %v, want ErrUnsupportedModel", err)
	}
	if _, err := spawn("claude-fabel-5-2"); !errors.Is(err, ErrUnsupportedModel) {
		t.Fatalf("typo error = %v, want ErrUnsupportedModel", err)
	}
}

// Chat is the default mode: an inherited Chat falls back to a terminal session
// for an unlisted well-formed ID, while an explicit Chat request is refused.
func TestSpawnUnlistedClaudeModelFallsBackFromInheritedChatOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		requested domain.SessionMode
		wantMode  domain.SessionMode
		wantErr   bool
		stale     bool
		bedrock   bool
	}{
		{name: "inherited chat falls back to TUI", wantMode: domain.SessionModeTUI},
		{name: "inherited chat falls back to TUI on a stale catalog", wantMode: domain.SessionModeTUI, stale: true},
		{name: "bedrock catalog refuses even inherited chat", wantErr: true, bedrock: true},
		{name: "explicit chat is refused", requested: domain.SessionModeChat, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &Manager{logger: slog.Default(), chat: &recordingLauncher{}, lookPath: exec.LookPath, executable: os.Executable, modelCatalog: tuningCatalog{catalog: ports.AgentModelCatalog{
				CustomModelEntry: ports.CustomModelEntryDirect, Stale: tc.stale,
				Models: []ports.AgentModelInfo{{ID: "claude-fable-5-1", Efforts: []string{"high"}}},
			}}}
			if tc.bedrock {
				m.modelCatalog = tuningCatalog{catalog: ports.AgentModelCatalog{
					CustomModelEntry: ports.CustomModelEntryDirect,
					Models:           []ports.AgentModelInfo{{ID: "anthropic.claude-opus-5-5-v1:0", Efforts: []string{"high"}}},
				}}
			}
			st := &spawnState{
				cfg: ports.SpawnConfig{
					ProjectID: "p", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
					RequestedMode: tc.requested, AgentConfig: ports.AgentConfig{Model: "claude-fable-5-2"},
				},
				project: domain.ProjectRecord{Config: domain.ProjectConfig{DefaultSessionMode: domain.SessionModeChat}},
			}
			exit := m.spawnResolveModelAndMode(context.Background(), st)
			if tc.wantErr {
				if exit == nil || !errors.Is(exit.err, ErrUnsupportedModel) {
					t.Fatalf("exit = %+v, want ErrUnsupportedModel", exit)
				}
				return
			}
			if exit != nil && errors.Is(exit.err, ErrUnsupportedModel) {
				t.Fatalf("inherited chat refused the unlisted ID: %v", exit.err)
			}
			if st.cfg.RequestedMode != tc.wantMode {
				t.Fatalf("mode = %q, want %q (exit %+v)", st.cfg.RequestedMode, tc.wantMode, exit)
			}
		})
	}
}
