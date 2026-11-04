package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/codex"
	"tallpine.eu/tallpine/backend/internal/adapters/agent/kiro"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// Security mode wants a fence; a harness that cannot be fenced launches anyway
// and the gap is recorded, while a fenced Claude on macOS has none.
func TestFenceGapFactsUnderSecurityMode(t *testing.T) {
	m := New(Deps{Agents: testAgentRegistry{domain.HarnessClaudeCode: claudecode.New(), domain.HarnessCodex: codex.New(), domain.HarnessKiro: kiro.New()}, Store: newFakeStore()})
	engine, _ := policytest.Engine(t, map[string]map[string]any{"securityMode": {"enabled": true}})
	m.SetAgentPolicy(engine)
	d := m.decide(context.Background(), "mer", domain.KindWorker, domain.HarnessCodex, "s-1")
	withFenceGOOS(t, "darwin")
	if gap := m.fenceGapFacts(d, domain.HarnessClaudeCode, ""); gap != nil {
		t.Fatalf("fenced claude on darwin has a gap: %v", gap)
	}
	if gap := m.fenceGapFacts(d, domain.HarnessKiro, ""); statefence.Available("darwin") && gap != nil {
		t.Fatalf("fenced kiro on darwin has a gap: %v", gap)
	}
	if err := m.admitTerminalFence(d, domain.HarnessCodex, domain.PermissionModeAuto); err != nil {
		t.Fatalf("codex must launch under Security mode: %v", err)
	}
	gap := m.fenceGapFacts(d, domain.HarnessCodex, domain.PermissionModeAuto)["protectState"]["fenceGap"]
	if s, _ := gap.(string); !strings.Contains(s, "codex") {
		t.Fatalf("codex gap = %v", gap)
	}
	// Only a Codex launch with no inner sandbox (bypass, default, empty) is
	// fenced; terminal and chat sessions both ask the terminal adapter.
	for _, mode := range []domain.PermissionMode{"", domain.PermissionModeDefault, domain.PermissionModeAcceptEdits, domain.PermissionModeAuto, domain.PermissionModeBypassPermissions} {
		fenced := mode != domain.PermissionModeAcceptEdits && mode != domain.PermissionModeAuto && statefence.Available("darwin")
		if gap := m.fenceGapFacts(d, domain.HarnessCodex, mode); (gap == nil) != fenced {
			t.Errorf("codex %q on darwin: gap = %v, fenced want %v", mode, gap, fenced)
		}
	}
	withFenceGOOS(t, "linux")
	if m.fenceGapFacts(d, domain.HarnessClaudeCode, "") == nil {
		t.Fatal("claude on linux must report a gap")
	}
	off, _ := policytest.Engine(t, nil)
	m.SetAgentPolicy(off)
	if m.fenceGapFacts(m.decide(context.Background(), "mer", domain.KindWorker, domain.HarnessCodex, "s-2"), domain.HarnessCodex, "") != nil {
		t.Fatal("mode off must report no gap")
	}
}

// The spawn path persists the gap in the session's decision record.
func TestSpawnRecordsFenceGap(t *testing.T) {
	rec, st := spawnWithPolicy(t, map[string]map[string]any{"securityMode": {"enabled": true}})
	if raw := string(st.policyRecord(rec.ID)); !strings.Contains(raw, `"fenceGap"`) {
		t.Fatalf("record lacks fenceGap: %s", raw)
	}
}

// A restored (reconciled) session records the gap too, and a fenced Claude on
// darwin records none.
func TestReconcileRecordsFenceGapOnlyWhenUnfenced(t *testing.T) {
	for _, tc := range []struct {
		name  string
		agent ports.Agent
		gap   bool
	}{{"unfenced", &recordingAgent{}, true}, {"fenced", fencingAgent{&recordingAgent{}}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			withFenceGOOS(t, "darwin")
			st := newFakeStore()
			st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
			m := New(Deps{Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}}, Agents: singleAgent{agent: tc.agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
				LookPath: func(string) (string, error) { return "/bin/true", nil }})
			engine, _ := policytest.Engine(t, map[string]map[string]any{"securityMode": {"enabled": true}})
			m.SetAgentPolicy(engine)
			rec := domain.SessionRecord{ID: "s1", ProjectID: "p1", Harness: domain.HarnessClaudeCode,
				Metadata: domain.SessionMetadata{Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1", AgentSessionID: "agent-s1"}}
			st.sessions[rec.ID] = rec
			if err := m.reconcileLive(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(st.policyRecord("s1")), `"fenceGap"`); got != tc.gap {
				t.Fatalf("fenceGap recorded = %v, want %v: %s", got, tc.gap, st.policyRecord("s1"))
			}
		})
	}
}

func TestSpawnFencedClaudeRecordsNoGap(t *testing.T) {
	withFenceGOOS(t, "darwin")
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: fencingAgent{&recordingAgent{}}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	engine, _ := policytest.Engine(t, map[string]map[string]any{"securityMode": {"enabled": true}})
	m.SetAgentPolicy(engine)
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker,
		AgentConfig: ports.AgentConfig{Permissions: domain.PermissionModeBypassPermissions}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(st.policyRecord(rec.ID)), `"fenceGap"`) {
		t.Fatal("fenced spawn recorded a gap")
	}
}
