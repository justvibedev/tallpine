package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// The plan service's mode hook reads the project's planningMode setting (the
// production path of "always" and "off"), and a managed lock wins over it.
func TestPlanModeSource_ReadsProjectSetting(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for id, mode := range map[string]domain.PlanningMode{"pa": domain.PlanningAlways, "po": domain.PlanningOff, "pn": ""} {
		rec := domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{PlanningMode: mode}}
		if err := store.UpsertProject(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := newAgentPolicyEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	src := planModeSource{store: store, policy: engine}
	for id, want := range map[string]domain.PlanningMode{"pa": domain.PlanningAlways, "po": domain.PlanningOff, "pn": domain.PlanningAuto, "missing": domain.PlanningAuto} {
		if got := src.Mode(ctx, domain.ProjectID(id)); got != want {
			t.Errorf("Mode(%s) = %q, want %q", id, got, want)
		}
	}
	// No engine wired: the project's setting still applies.
	if got := (planModeSource{store: store}).Mode(ctx, "po"); got != domain.PlanningOff {
		t.Errorf("without a policy engine, Mode(po) = %q", got)
	}
}

// planAutoApprove reaches the plan service only when the project turned it on
// and no managed policy locks it off (a bad managed value locks it off too);
// without a policy engine it is off.
func TestPlanModeSource_AutoApprove(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	yes, no := true, false
	for id, on := range map[string]*bool{"on": &yes, "off": &no, "unset": nil} {
		rec := domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{PlanAutoApprove: on}}
		if err := store.UpsertProject(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	engineWith := func(managed string) *agentpolicy.Engine {
		t.Helper()
		opts := []agentpolicy.Option{agentpolicy.WithEnv(func(string) (string, bool) { return "", false })}
		if managed != "" {
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(managed), 0o600); err != nil {
				t.Fatal(err)
			}
			opts = append(opts, agentpolicy.WithManagedFile(path, os.ReadFile))
		}
		e, err := agentpolicy.New(rules.All(), &policytest.Store{}, opts...)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	cases := []struct {
		name    string
		managed string
		project string
		want    bool
	}{
		{"setting on", "", "on", true},
		{"setting off", "", "off", false},
		{"unset inherits built-in off", "", "unset", false},
		{"unknown project", "", "missing", false},
		{"managed unlocked", `{"schemaVersion":2,"rules":{"planner":{"autoApprove":""}}}`, "on", true},
		{"managed lock off", `{"schemaVersion":2,"rules":{"planner":{"autoApprove":"off"}}}`, "on", false},
		{"managed invalid value locks off", `{"schemaVersion":2,"rules":{"planner":{"autoApprove":"maybe"}}}`, "on", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := planModeSource{store: store, policy: engineWith(c.managed)}
			if got := src.AutoApprove(ctx, domain.ProjectID(c.project)); got != c.want {
				t.Fatalf("AutoApprove(%s) = %v, want %v", c.project, got, c.want)
			}
		})
	}
	if (planModeSource{store: store}).AutoApprove(ctx, "on") {
		t.Fatal("without a policy engine auto-approve must be off")
	}
}

// An unset project follows the app default; an explicit project value beats it;
// a managed lock off wins over everything; a failed defaults read is off.
func TestPlanModeSource_AutoApproveAppDefault(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	yes, no := true, false
	for id, on := range map[string]*bool{"on": &yes, "off": &no, "unset": nil} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{PlanAutoApprove: on}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetWorkflowDefaults(ctx, domain.WorkflowDefaults{PlanningMode: domain.PlanningAuto, PlanAutoApprove: true}, now); err != nil {
		t.Fatal(err)
	}
	engine, err := newAgentPolicyEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	src := planModeSource{store: store, policy: engine}
	for id, want := range map[string]bool{"unset": true, "on": true, "off": false, "missing": false} {
		if got := src.AutoApprove(ctx, domain.ProjectID(id)); got != want {
			t.Errorf("AutoApprove(%s) with app default on = %v, want %v", id, got, want)
		}
	}
	if (planModeSource{store: store}).AutoApprove(ctx, "unset") {
		t.Error("without a policy engine auto-approve must be off")
	}
	// A failed read gives the built-in defaults, never an enabled approval.
	_ = store.Close()
	if (planModeSource{store: store, policy: engine}).AutoApprove(ctx, "unset") {
		t.Error("a failed read must mean off")
	}
}

// The app default planning mode applies to unset projects; the user layer cannot
// lock the mode any more.
func TestPlanModeSource_ModeAppDefault(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for id, mode := range map[string]domain.PlanningMode{"unset": "", "auto": domain.PlanningAuto, "off": domain.PlanningOff} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{PlanningMode: mode}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SetWorkflowDefaults(ctx, domain.WorkflowDefaults{PlanningMode: domain.PlanningAlways}, now); err != nil {
		t.Fatal(err)
	}
	engine, err := newAgentPolicyEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	src := planModeSource{store: store, policy: engine}
	for id, want := range map[string]domain.PlanningMode{"unset": domain.PlanningAlways, "auto": domain.PlanningAuto, "off": domain.PlanningOff} {
		if got := src.Mode(ctx, domain.ProjectID(id)); got != want {
			t.Errorf("Mode(%s) = %q, want %q", id, got, want)
		}
	}
}

// planInPlace reaches the plan service only when the project turned it on and
// no managed policy locks it off; without a policy engine it is off.
func TestPlanModeSource_InPlace(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for id, on := range map[string]bool{"on": true, "off": false} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{PlanInPlace: on}}); err != nil {
			t.Fatal(err)
		}
	}
	engineWith := func(managed string) *agentpolicy.Engine {
		t.Helper()
		opts := []agentpolicy.Option{agentpolicy.WithEnv(func(string) (string, bool) { return "", false })}
		if managed != "" {
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(managed), 0o600); err != nil {
				t.Fatal(err)
			}
			opts = append(opts, agentpolicy.WithManagedFile(path, os.ReadFile))
		}
		e, err := agentpolicy.New(rules.All(), &policytest.Store{}, opts...)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for _, c := range []struct {
		name, managed, project string
		want                   bool
	}{
		{"setting on", "", "on", true},
		{"setting off", "", "off", false},
		{"unset inherits built-in off", "", "unset", false},
		{"unknown project", "", "missing", false},
		{"managed lock off", `{"schemaVersion":2,"rules":{"planner":{"inPlace":"off"}}}`, "on", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := (planModeSource{store: store, policy: engineWith(c.managed)}).InPlace(ctx, domain.ProjectID(c.project)); got != c.want {
				t.Fatalf("InPlace(%s) = %v, want %v", c.project, got, c.want)
			}
		})
	}
	if (planModeSource{store: store}).InPlace(ctx, "on") {
		t.Fatal("without a policy engine plan-in-place must be off")
	}
}

// The eval waiver (an orchestrator approves without the owner's message) needs
// the project marked eval and no managed lock on plan auto-approval; without a
// policy engine it is off.
func TestPlanModeSource_Eval(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	for id, on := range map[string]bool{"eval": true, "real": false} {
		if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: id, Path: "/tmp/" + id, RegisteredAt: now, Config: domain.ProjectConfig{Eval: on}}); err != nil {
			t.Fatal(err)
		}
	}
	engineWith := func(managed string) *agentpolicy.Engine {
		t.Helper()
		opts := []agentpolicy.Option{agentpolicy.WithEnv(func(string) (string, bool) { return "", false })}
		if managed != "" {
			path := filepath.Join(t.TempDir(), "policy.json")
			if err := os.WriteFile(path, []byte(managed), 0o600); err != nil {
				t.Fatal(err)
			}
			opts = append(opts, agentpolicy.WithManagedFile(path, os.ReadFile))
		}
		e, err := agentpolicy.New(rules.All(), &policytest.Store{}, opts...)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	for _, c := range []struct {
		name, managed, project string
		want                   bool
	}{
		{"marked eval", "", "eval", true},
		{"not marked", "", "real", false},
		{"unknown project", "", "missing", false},
		{"managed autoApprove lock off wins", `{"schemaVersion":2,"rules":{"planner":{"autoApprove":"off"}}}`, "eval", false},
		{"managed lock unlocked", `{"schemaVersion":2,"rules":{"planner":{"autoApprove":""}}}`, "eval", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := (planModeSource{store: store, policy: engineWith(c.managed)}).Eval(ctx, domain.ProjectID(c.project)); got != c.want {
				t.Fatalf("Eval(%s) = %v, want %v", c.project, got, c.want)
			}
		})
	}
	if (planModeSource{store: store}).Eval(ctx, "eval") {
		t.Fatal("without a policy engine the eval waiver must be off")
	}
}

// A closed eval waiver (migrated from a user autoApprove off lock) keeps Eval
// false for a project marked eval at any time.
func TestPlanModeSource_EvalWaiverClosed(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "ev", Path: "/tmp/ev", RegisteredAt: now, Config: domain.ProjectConfig{Eval: true}}); err != nil {
		t.Fatal(err)
	}
	engine, err := newAgentPolicyEngine(store)
	if err != nil {
		t.Fatal(err)
	}
	src := planModeSource{store: store, policy: engine}
	if !src.Eval(ctx, "ev") {
		t.Fatal("eval project without a closed gate must get the waiver")
	}
	d := domain.DefaultWorkflowDefaults()
	d.EvalWaiverClosed = true
	if err := store.SetWorkflowDefaults(ctx, d, now); err != nil {
		t.Fatal(err)
	}
	if src.Eval(ctx, "ev") {
		t.Fatal("eval waiver must stay closed after the gate is set")
	}
}
