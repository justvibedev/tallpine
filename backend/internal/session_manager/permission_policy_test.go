package sessionmanager

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func capSettings(level permissioncap.Level) map[string]map[string]any {
	return map[string]map[string]any{permissioncap.Name: {permissioncap.FieldMax: string(level)}}
}

// spawnBypassWorker spawns a worker that asks for bypass under an optional cap
// and returns the session, the runtime config and the launch config the agent
// adapter saw.
func spawnBypassWorker(t *testing.T, settings map[string]map[string]any) (domain.SessionRecord, *fakeRuntime, ports.LaunchConfig) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	agent := &recordingAgent{}
	rt := &fakeRuntime{}
	m := New(Deps{Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	if settings != nil {
		engine, _ := policytest.Engine(t, settings)
		m.SetAgentPolicy(engine)
	}
	rec, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{
		ProjectID: "mer", Kind: domain.KindWorker,
		AgentConfig: ports.AgentConfig{Permissions: domain.PermissionModeBypassPermissions},
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec, rt, agent.lastLaunch
}

func TestSpawnWithBypass_CappedByPolicy(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		level permissioncap.Level
		want  domain.PermissionMode
	}{
		{permissioncap.Auto, domain.PermissionModeAuto},
		{permissioncap.AcceptEdits, domain.PermissionModeAcceptEdits},
	} {
		rec, rt, launch := spawnBypassWorker(t, capSettings(tc.level))
		if rec.Metadata.Permissions != tc.want {
			t.Errorf("cap %s: persisted permissions = %q, want %q", tc.level, rec.Metadata.Permissions, tc.want)
		}
		if got := rt.lastCfg.Env[EnvPermissionMode]; got != string(tc.want) {
			t.Errorf("cap %s: launch %s = %q, want %q", tc.level, EnvPermissionMode, got, tc.want)
		}
		if launch.Permissions != tc.want {
			t.Errorf("cap %s: adapter launch permissions = %q, want %q", tc.level, launch.Permissions, tc.want)
		}
		// The adapter receives the decision itself, not just a lowered mode, so it
		// can apply the effects only it knows (a pinned sandbox, an explicit default).
		if !permissioncap.Of(launch.Policy).ForbidsBypass() || launch.Policy.Session().Project != "mer" {
			t.Errorf("cap %s: the launch carries no policy decision: %+v", tc.level, launch.Policy.Session())
		}
	}
}

func TestSpawnWithBypass_UnchangedWithoutPolicy(t *testing.T) {
	t.Parallel()
	for _, settings := range []map[string]map[string]any{nil, capSettings(permissioncap.None)} {
		rec, rt, launch := spawnBypassWorker(t, settings)
		if rec.Metadata.Permissions != domain.PermissionModeBypassPermissions ||
			rt.lastCfg.Env[EnvPermissionMode] != string(domain.PermissionModeBypassPermissions) ||
			launch.Permissions != domain.PermissionModeBypassPermissions || permissioncap.Of(launch.Policy).ForbidsBypass() {
			t.Fatalf("settings %v changed default behaviour: %q / %q / %q", settings, rec.Metadata.Permissions, rt.lastCfg.Env[EnvPermissionMode], launch.Permissions)
		}
	}
}

func TestRestoreBypassSession_CappedByPolicy(t *testing.T) {
	t.Parallel()
	rec := domain.SessionRecord{
		ID: "mer-9", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex,
		Metadata: domain.SessionMetadata{Permissions: domain.PermissionModeBypassPermissions},
	}
	if got := restoredAgentConfigUnder(agentpolicy.Decision{}, rec, domain.ProjectConfig{}).Permissions; got != domain.PermissionModeBypassPermissions {
		t.Fatalf("no policy must keep bypass, got %q", got)
	}
	d := policytest.Cap(t, permissioncap.Auto)
	if got := restoredAgentConfigUnder(d, rec, domain.ProjectConfig{}).Permissions; got != domain.PermissionModeAuto {
		t.Fatalf("restored bypass session must be capped, got %q", got)
	}
	// Codex default means full access, so the cap lowers it too.
	rec.Metadata.Permissions = domain.PermissionModeDefault
	if got := restoredAgentConfigUnder(d, rec, domain.ProjectConfig{}).Permissions; got != domain.PermissionModeAuto {
		t.Fatalf("codex default must be capped, got %q", got)
	}
}

// A project's own override tightens the cap for that project's sessions only.
func TestSpawnUsesTheProjectsOverride(t *testing.T) {
	t.Parallel()
	settings := map[string]map[string]any{}
	engine, store := policytest.Engine(t, settings)
	if err := engine.Update(context.Background(), "mer", agentpolicy.Update{permissioncap.Name: {permissioncap.FieldMax: json.RawMessage(`"accept_edits"`)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(store.Data) == 0 {
		t.Fatal("the override was not stored")
	}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	st.projects["other"] = domain.ProjectRecord{ID: "other", Config: testRoleAgents()}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	m.SetAgentPolicy(engine)
	for project, want := range map[domain.ProjectID]domain.PermissionMode{"mer": domain.PermissionModeAcceptEdits, "other": domain.PermissionModeBypassPermissions} {
		if _, _, _, err := m.Spawn(context.Background(), ports.SpawnConfig{ProjectID: project, Kind: domain.KindWorker, AgentConfig: ports.AgentConfig{Permissions: domain.PermissionModeBypassPermissions}}); err != nil {
			t.Fatal(err)
		}
		if agent.lastLaunch.Permissions != want {
			t.Errorf("project %s launched with %q, want %q", project, agent.lastLaunch.Permissions, want)
		}
	}
}
