package settingsview

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/managedcfg"
	"tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
)

type fakeApp struct{ s settingssvc.Snapshot }

func (f fakeApp) Get(context.Context) (settingssvc.Snapshot, error) { return f.s, nil }

type fakeCleanup struct{}

func (fakeCleanup) Status(context.Context) (cleanup.Status, error) {
	return cleanup.Status{Policy: cleanup.DefaultPolicy()}, nil
}

type fakePolicy struct{ projects []string }

func (f *fakePolicy) Describe(_ context.Context, project string) agentpolicy.View {
	f.projects = append(f.projects, project)
	return agentpolicy.View{Rules: []agentpolicy.RuleView{{Name: "permissionCap", Fields: []agentpolicy.FieldView{
		{Name: "max", Value: json.RawMessage(`"auto"`), Source: agentpolicy.SourceManaged, Locked: true},
	}}, {Name: "orchestratorApprovals", Fields: []agentpolicy.FieldView{
		{Name: "enabled", Value: json.RawMessage(`true`), Source: agentpolicy.SourceUser},
	}}}}
}

type fakeProjects struct {
	cfg *domain.ProjectConfig
	err error
}

func (f fakeProjects) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return projectsvc.GetResult{Project: &projectsvc.Project{Config: f.cfg}}, f.err
}

func byKey(values []Value) map[settingsreg.Key]Value {
	out := map[settingsreg.Key]Value{}
	for _, v := range values {
		out[v.Key] = v
	}
	return out
}

func freshSnapshot() settingssvc.Snapshot {
	return settingssvc.Snapshot{
		DefaultSessionMode: domain.SessionModeChat, ChatHibernation: domain.DefaultChatHibernationPolicy(),
		VersionRefresh: true, StorageCompaction: true, ProviderEventRetentionDays: settingssvc.DefaultProviderEventRetentionDays,
		HarnessDefaults: map[string]domain.HarnessDefault{},
	}
}

func TestAppValuesSourcesLocksAndCoverage(t *testing.T) {
	snap := freshSnapshot()
	snap.StorageCompaction = false
	policy := &fakePolicy{}
	svc := &Service{
		AppSettings: fakeApp{snap}, CleanupPolicy: fakeCleanup{}, AgentPolicy: policy,
		LookupEnv: func(name string) (string, bool) { return "off", name == "TALLPINE_VERSION_REFRESH" },
	}
	values, err := svc.App(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(values)
	for _, def := range settingsreg.Default().Scope(settingsreg.ScopeApp) {
		if _, ok := got[def.Key]; !ok {
			t.Errorf("%s missing", def.Key)
		}
	}
	if v := got["app.storageCompaction"]; v.Source != SourceUser || string(v.Value) != "false" || !slices.Contains(v.Writable, settingsreg.ActorUser) {
		t.Errorf("storageCompaction = %+v", v)
	}
	if v := got["app.providerEventRetentionDays"]; v.Source != SourceDefault || string(v.Value) != "14" {
		t.Errorf("retention = %+v", v)
	}
	if v := got["app.versionRefresh"]; !v.Locked || v.LockedBy != "TALLPINE_VERSION_REFRESH" || v.Source != SourceEnv ||
		string(v.Value) != "false" || len(v.Writable) != 0 {
		t.Errorf("versionRefresh = %+v", v)
	}
	if v := got["app.cleanup.automatic"]; v.Source != SourceDefault {
		t.Errorf("cleanup.automatic = %+v", v)
	}
	if v := got["policy.permissionCap.max"]; !v.Locked || v.LockedBy != "managed" || len(v.Writable) != 0 {
		t.Errorf("permissionCap.max = %+v", v)
	}
	if v := got["policy.orchestratorApprovals.enabled"]; v.Locked || v.Source != "user" || !slices.Equal(v.Writable, []settingsreg.Actor{settingsreg.ActorUser}) {
		t.Errorf("orchestratorApprovals.enabled = %+v", v)
	}
	if !slices.Equal(policy.projects, []string{""}) {
		t.Errorf("policy described for %v", policy.projects)
	}
}

func TestProjectValuesReadNestedKeysAndWriters(t *testing.T) {
	cfg := &domain.ProjectConfig{AgentRules: "Be brief.", Orchestrator: domain.RoleOverride{AutoCompactWindow: "1m"}}
	policy := &fakePolicy{}
	values, err := (&Service{Projects: fakeProjects{cfg: cfg}, AgentPolicy: policy}).Project(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(values)
	if n := len(settingsreg.Default().Scope(settingsreg.ScopeProject)); len(values)-2 != n {
		t.Errorf("project values = %d, want %d project keys + 2 policy", len(values), n)
	}
	if v := got["project.agentRules"]; v.Source != SourceProject || string(v.Value) != `"Be brief."` ||
		!slices.Contains(v.Writable, settingsreg.ActorOrchestrator) {
		t.Errorf("agentRules = %+v", v)
	}
	if v := got["project.orchestrator.autoCompactWindow"]; v.Source != SourceProject || string(v.Value) != `"1m"` {
		t.Errorf("autoCompactWindow = %+v", v)
	}
	if v := got["project.noCI"]; v.Source != SourceDefault || string(v.Value) != "false" || !slices.Contains(v.Writable, settingsreg.ActorOrchestrator) {
		t.Errorf("noCI = %+v", v)
	}
	if !slices.Equal(policy.projects, []string{"app"}) {
		t.Errorf("policy described for %v", policy.projects)
	}
}

func TestProjectValuesPropagateOwnerErrors(t *testing.T) {
	boom := errors.New("unknown project")
	if _, err := (&Service{Projects: fakeProjects{err: boom}}).Project(context.Background(), "x"); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

// TALLPINE_VERSION_REFRESH is a kill switch: only "off" forces and locks the
// setting; "on", empty or invalid leave it to the user, as in the daemon.
func TestVersionRefreshEnvIsAKillSwitch(t *testing.T) {
	cases := []struct {
		env    string
		set    bool
		value  string
		source string
		locked bool
	}{
		{"off", true, "false", SourceEnv, true},
		{"0", true, "false", SourceEnv, true},
		{"on", true, "true", SourceDefault, false},
		{"", true, "true", SourceDefault, false},
		{"maybe", true, "true", SourceDefault, false},
		{"", false, "true", SourceDefault, false},
	}
	for _, c := range cases {
		svc := &Service{AppSettings: fakeApp{freshSnapshot()},
			LookupEnv: func(name string) (string, bool) { return c.env, c.set && name == "TALLPINE_VERSION_REFRESH" }}
		values, err := svc.App(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		v := byKey(values)["app.versionRefresh"]
		if string(v.Value) != c.value || v.Source != c.source || v.Locked != c.locked || (len(v.Writable) == 0) != c.locked {
			t.Errorf("env %q (set=%v): %+v", c.env, c.set, v)
		}
	}
}

// A struct-typed project field that was never set is reported as the default,
// not as a project value; one with a set leaf is the project's.
func TestProjectValuesTreatEmptyStructsAsUnset(t *testing.T) {
	values, err := (&Service{Projects: fakeProjects{cfg: &domain.ProjectConfig{}}}).Project(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Source != SourceDefault {
			t.Errorf("%s: unset project field reported as %s (%s)", v.Key, v.Source, v.Value)
		}
	}
	cfg := &domain.ProjectConfig{AgentConfig: domain.AgentConfig{Permissions: domain.PermissionModeAuto}}
	values, err = (&Service{Projects: fakeProjects{cfg: cfg}}).Project(context.Background(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if v := byKey(values)["project.agentConfig"]; v.Source != SourceProject {
		t.Errorf("set agentConfig = %+v", v)
	}
}

func TestManagedValuesLockAppAndProjectKeys(t *testing.T) {
	cfg := &domain.ProjectConfig{Models: domain.ModelPolicy{CodexDefault: "mine"}}
	managed := managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{
			"app.providerEventRetentionDays": json.RawMessage("3"),
			"project.models":                 nil, // locked, value unusable: keeps the stored one
		}
	})
	svc := &Service{AppSettings: fakeApp{freshSnapshot()}, CleanupPolicy: fakeCleanup{}, Projects: fakeProjects{cfg: cfg}, Managed: managed}
	app, err := svc.App(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byKey(app)["app.providerEventRetentionDays"]
	if !got.Locked || got.LockedBy != SourceManaged || got.Source != SourceManaged || string(got.Value) != "3" || len(got.Writable) != 0 {
		t.Fatalf("retention = %+v, want locked by managed at 3 with no writers", got)
	}
	if v := byKey(app)["app.cleanup.automatic"]; v.Locked {
		t.Error("an unset key must not be locked")
	}
	proj, err := svc.Project(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	models := byKey(proj)["project.models"]
	if !models.Locked || len(models.Writable) != 0 || !json.Valid(models.Value) || string(models.Value) == "null" {
		t.Fatalf("models = %+v, want locked and keeping the stored value", models)
	}
}

func TestNestedKeysInheritTheLockOfALockedParent(t *testing.T) {
	cfg := &domain.ProjectConfig{}
	cfg.Orchestrator.AutoCompactWindow = "200k"
	managed := managedcfg.New(func() map[string]json.RawMessage {
		return map[string]json.RawMessage{"project.orchestrator": json.RawMessage(`{"autoCompactWindow":"1m"}`)}
	})
	svc := &Service{Projects: fakeProjects{cfg: cfg}, Managed: managed}
	proj, err := svc.Project(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []settingsreg.Key{"project.orchestrator", "project.orchestrator.autoCompactWindow", "project.orchestrator.idleCompactAt"} {
		v := byKey(proj)[key]
		if !v.Locked || v.LockedBy != SourceManaged || len(v.Writable) != 0 {
			t.Errorf("%s = %+v, want locked by managed with no writers", key, v)
		}
	}
	if v := byKey(proj)["project.orchestrator.autoCompactWindow"]; string(v.Value) != `"1m"` {
		t.Errorf("nested value = %s, want the managed \"1m\"", v.Value)
	}
	if byKey(proj)["project.worker"].Locked {
		t.Error("an unrelated key was locked")
	}
}
