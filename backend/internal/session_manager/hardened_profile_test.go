package sessionmanager

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

const hardenedDoc = `{"schemaVersion":2,"profile":"hardened"}`

// withFenceGOOS swaps the host OS launches are admitted for. Tests using it do
// not run in parallel.
func withFenceGOOS(t *testing.T, goos string) {
	t.Helper()
	prev := fenceGOOS
	fenceGOOS = goos
	t.Cleanup(func() { fenceGOOS = prev })
}

// fencingAgent is a recording agent that can carry the state fence on macOS,
// as the Claude Code adapter does.
type fencingAgent struct{ *recordingAgent }

func (fencingAgent) FencesState(goos string) bool { return claudecode.FencesState(goos) }

// The terminal fence check uses the real adapters' capability: only Claude Code
// on macOS is admitted, for every session kind.
func TestAdmitTerminalFenceUnderProfile(t *testing.T) {
	agents := testAgentRegistry{
		domain.HarnessClaudeCode: claudecode.New(),
		domain.HarnessCodex:      codex.New(),
		domain.HarnessKiro:       kiro.New(),
		domain.HarnessDeepSeek:   deepseekharness.New(),
	}
	m := New(Deps{Agents: agents, Store: newFakeStore()})
	engine, _ := policytest.Managed(t, hardenedDoc, nil)
	m.SetAgentPolicy(engine)
	for _, goos := range []string{"darwin", "linux", "windows"} {
		withFenceGOOS(t, goos)
		for _, kind := range []domain.SessionKind{domain.KindWorker, domain.KindOrchestrator} {
			for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessKiro, domain.HarnessDeepSeek, "mystery"} {
				for _, mode := range []domain.PermissionMode{domain.PermissionModeAuto, domain.PermissionModeBypassPermissions} {
					err := m.admitTerminalFence(m.decide(context.Background(), "mer", kind, harness, "s-1"), harness, mode)
					admitted := goos == "darwin" && (harness == domain.HarnessClaudeCode ||
						(harness == domain.HarnessKiro || (harness == domain.HarnessCodex || harness == domain.HarnessDeepSeek) && mode == domain.PermissionModeBypassPermissions) && statefence.Available(goos))
					if admitted != (err == nil) || (err != nil && !agentpolicy.IsFenceRequired(err)) {
						t.Errorf("%s %s %s %s: err = %v, admitted want %v", goos, kind, harness, mode, err, admitted)
					}
				}
			}
		}
	}
	// Without the profile nothing is refused, protectState on or not.
	plain, _ := policytest.Managed(t, `{"schemaVersion":2,"rules":{"protectState":{"mode":"on"}}}`, nil)
	m.SetAgentPolicy(plain)
	withFenceGOOS(t, "linux")
	if err := m.admitTerminalFence(m.decide(context.Background(), "mer", domain.KindWorker, domain.HarnessCodex, ""), domain.HarnessCodex, ""); err != nil {
		t.Fatalf("no profile refused codex: %v", err)
	}
}

func hardenedManager(t *testing.T, agent ports.Agent) (*Manager, *fakeStore, *fakeRuntime) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	engine, _ := policytest.Managed(t, hardenedDoc, nil)
	m.SetAgentPolicy(engine)
	return m, st, rt
}

func TestSpawnUnderProfileRefusesUnfenceable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		agent   ports.Agent
		goos    string
		harness domain.AgentHarness
		refused bool
	}{
		{"codex darwin", &recordingAgent{}, "darwin", domain.HarnessCodex, true},
		{"unknown harness darwin", &recordingAgent{}, "darwin", "mystery", true},
		{"claude linux", fencingAgent{&recordingAgent{}}, "linux", domain.HarnessClaudeCode, true},
		{"claude windows", fencingAgent{&recordingAgent{}}, "windows", domain.HarnessClaudeCode, true},
		{"claude darwin", fencingAgent{&recordingAgent{}}, "darwin", domain.HarnessClaudeCode, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withFenceGOOS(t, tc.goos)
			m, _, rt := hardenedManager(t, tc.agent)
			_, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: tc.harness, RequestedMode: domain.SessionModeTUI})
			if tc.refused {
				if !agentpolicy.IsFenceRequired(err) {
					t.Fatalf("err = %v, want fence refusal", err)
				}
				if len(rt.lastCfg.Argv) != 0 {
					t.Fatalf("refused spawn still launched %v", rt.lastCfg.Argv)
				}
				return
			}
			if err != nil {
				t.Fatalf("claude on darwin refused: %v", err)
			}
		})
	}
}

func TestRestoreUnderProfileRefusesUnfenceable(t *testing.T) {
	withFenceGOOS(t, "darwin")
	m, st, rt := hardenedManager(t, &recordingAgent{})
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", AgentSessionID: "agent-x"})
	rec := st.sessions["mer-1"]
	rec.Harness = domain.HarnessCodex
	st.sessions["mer-1"] = rec
	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("restore err = %v, want fence refusal", err)
	}
	if len(rt.lastCfg.Argv) != 0 {
		t.Fatalf("refused restore launched %v", rt.lastCfg.Argv)
	}
}

// Account moves and chat->terminal switches preflight through
// preflightTerminalLaunch before anything stops.
func TestTerminalPreflightUnderProfileRefusesUnfenceable(t *testing.T) {
	withFenceGOOS(t, "darwin")
	m, st, _ := hardenedManager(t, &recordingAgent{})
	rec := domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessKiro}
	err := m.preflightTerminalLaunch(context.Background(), m.decideFor(context.Background(), rec), &recordingAgent{}, rec, st.projects["mer"], "")
	if !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("preflight err = %v, want fence refusal", err)
	}
}

// Codex never runs with approval never / danger-full-access under the profile,
// whatever it asked for, on start and restore.
func TestCodexCappedUnderProfile(t *testing.T) {
	d := policytest.Hardened(t)
	for _, asked := range []domain.PermissionMode{domain.PermissionModeBypassPermissions, domain.PermissionModeDefault} {
		rec := domain.SessionRecord{ID: "c", Harness: domain.HarnessCodex, Metadata: domain.SessionMetadata{Permissions: asked}}
		if got := restoredAgentConfigUnder(d, rec, domain.ProjectConfig{}).Permissions; got != domain.PermissionModeAuto {
			t.Errorf("restore %s -> %s, want auto", asked, got)
		}
		if got := applySpawnAgentConfigUnder(d, domain.HarnessCodex, ports.AgentConfig{}, ports.AgentConfig{Permissions: asked}).Permissions; got != domain.PermissionModeAuto {
			t.Errorf("spawn %s -> %s, want auto", asked, got)
		}
	}
}
