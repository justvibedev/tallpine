package sessionmanager

import (
	"context"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// bypassFencingAgent fences only bypass launches, like Codex, so a test sees
// which mode each seam asks about.
type bypassFencingAgent struct{ *recordingAgent }

func (bypassFencingAgent) FencesState(string) bool { return true }

func (bypassFencingAgent) FencesLaunch(_ string, mode ports.PermissionMode) bool {
	return mode == domain.PermissionModeBypassPermissions
}

func modeFenceManager(t *testing.T, settings map[string]map[string]any) (*Manager, *fakeStore) {
	t.Helper()
	withFenceGOOS(t, "darwin")
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	m := New(Deps{Runtime: &fakeRuntime{aliveByHandle: map[string]bool{}}, Agents: singleAgent{agent: bypassFencingAgent{&recordingAgent{}}}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	engine, _ := policytest.Engine(t, settings)
	m.SetAgentPolicy(engine)
	return m, st
}

// Spawn asks the adapter about the mode it launches in after the permission
// cap, not the one requested: a bypass request capped to auto is not fenced.
func TestSpawnFenceGapUsesPostCapMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[string]map[string]any
		gap      bool
	}{
		{"bypass", map[string]map[string]any{"securityMode": {"enabled": true}}, false},
		{"capped", map[string]map[string]any{"securityMode": {"enabled": true}, "permissionCap": {"max": "auto"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st := modeFenceManager(t, tc.settings)
			rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker,
				AgentConfig: ports.AgentConfig{Permissions: domain.PermissionModeBypassPermissions}})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(st.policyRecord(rec.ID)), `"fenceGap"`); got != tc.gap {
				t.Fatalf("fenceGap = %v, want %v: %s", got, tc.gap, st.policyRecord(rec.ID))
			}
		})
	}
}

// A restore re-caps the remembered mode with today's policy before asking.
func TestReconcileFenceGapUsesPostCapMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[string]map[string]any
		gap      bool
	}{
		{"bypass", map[string]map[string]any{"securityMode": {"enabled": true}}, false},
		{"capped", map[string]map[string]any{"securityMode": {"enabled": true}, "permissionCap": {"max": "auto"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st := modeFenceManager(t, tc.settings)
			rec := domain.SessionRecord{ID: "s1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
				Metadata: domain.SessionMetadata{Branch: "tp/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1", AgentSessionID: "agent-s1", Permissions: domain.PermissionModeBypassPermissions}}
			st.sessions[rec.ID] = rec
			if err := m.reconcileLive(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(st.policyRecord("s1")), `"fenceGap"`); got != tc.gap {
				t.Fatalf("fenceGap = %v, want %v: %s", got, tc.gap, st.policyRecord("s1"))
			}
		})
	}
}

// The launch env's fence git config follows the stored mode lowered by
// today's cap: a remembered bypass session now capped to auto is not fenced.
func TestFenceGitConfigUsesPostCapStoredMode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings map[string]map[string]any
		keys     bool
	}{
		{"bypass", map[string]map[string]any{"securityMode": {"enabled": true}}, true},
		{"capped", map[string]map[string]any{"securityMode": {"enabled": true}, "permissionCap": {"max": "auto"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st := modeFenceManager(t, tc.settings)
			st.sessions["s1"] = domain.SessionRecord{ID: "s1", ProjectID: "mer", Harness: domain.HarnessClaudeCode,
				Metadata: domain.SessionMetadata{Permissions: domain.PermissionModeBypassPermissions}}
			env := map[string]string{}
			m.applyFenceGitConfigFor("s1", "", env)
			if got := hasFenceGitKeys(env); got != tc.keys {
				t.Fatalf("fence git keys = %v, want %v: %v", got, tc.keys, env)
			}
		})
	}
}
