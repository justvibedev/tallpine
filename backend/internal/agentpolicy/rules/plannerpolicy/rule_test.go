package plannerpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/ruletest"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func decide(t *testing.T, managed string) Outcome {
	t.Helper()
	return Of(ruletest.Engine(t, Rule{}, managed).DecideStatic(context.Background()))
}

func TestPlannerPolicy_LocksModeAndModel(t *testing.T) {
	o := decide(t, `{"schemaVersion":2,"rules":{"planner":{"mode":"always","harness":"codex","model":"gpt-x"}}}`)
	if got := ResolvePlanningMode(o, domain.ProjectConfig{PlanningMode: domain.PlanningOff}, domain.WorkflowDefaults{}); got != domain.PlanningAlways {
		t.Fatalf("mode = %q, want the locked always over the project's off", got)
	}
	lock, ok := o.ModelLock()
	if !ok || lock.Harness != domain.HarnessCodex || lock.AgentConfig.Model != "gpt-x" {
		t.Fatalf("lock = %+v, %v", lock, ok)
	}
}

// Nothing in the rule turns planning off, or locks anything, by default.
func TestPlannerPolicy_EmptyDoesNotDisable(t *testing.T) {
	o := decide(t, "")
	if o != (Outcome{}) {
		t.Fatalf("default outcome = %+v, want the zero (unlocked) outcome", o)
	}
	if _, ok := o.ModelLock(); ok {
		t.Fatal("empty rule locked a model")
	}
}

func TestPlannerPolicy_RejectsUnknownHarness(t *testing.T) {
	if err := (Rule{}).Validate(agentpolicy.Settings{FieldHarness: []byte(`"claud-code"`)}); err == nil {
		t.Fatal("unknown harness accepted")
	}
	if err := (Rule{}).Validate(agentpolicy.Settings{FieldHarness: []byte(`"codex"`)}); err != nil {
		t.Fatalf("codex rejected: %v", err)
	}
	if err := (Rule{}).Validate(agentpolicy.Settings{FieldHarness: []byte(`""`)}); err != nil {
		t.Fatalf("empty rejected: %v", err)
	}
}

// autoApprove locks plan auto-approval off; empty (and no managed file) locks
// nothing, and an invalid managed value fails closed to the lock.
func TestRule_AutoApproveLock(t *testing.T) {
	for name, c := range map[string]struct {
		managed string
		locked  bool
	}{
		"no policy":      {"", false},
		"empty":          {`{"schemaVersion":2,"rules":{"planner":{"autoApprove":""}}}`, false},
		"off":            {`{"schemaVersion":2,"rules":{"planner":{"autoApprove":"off"}}}`, true},
		"invalid":        {`{"schemaVersion":2,"rules":{"planner":{"autoApprove":"maybe"}}}`, true},
		"mode unrelated": {`{"schemaVersion":2,"rules":{"planner":{"mode":"always"}}}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := decide(t, c.managed).AutoApproveLocked; got != c.locked {
				t.Fatalf("AutoApproveLocked = %v, want %v", got, c.locked)
			}
		})
	}
}

func TestSchemaDeclaresHarnessAndModelSources(t *testing.T) {
	fields := map[string]agentpolicy.Field{}
	for _, f := range (Rule{}).Schema().Fields {
		fields[f.Name] = f
	}
	if got := fields[FieldHarness].OptionSource; got != agentpolicy.SourceHarness {
		t.Fatalf("harness source = %q", got)
	}
	m := fields[FieldModel]
	if m.OptionSource != agentpolicy.SourceModel || m.SourceField != FieldHarness {
		t.Fatalf("model source = %q from %q", m.OptionSource, m.SourceField)
	}
}

func TestPlannerPolicy_InPlaceLock(t *testing.T) {
	for managed, want := range map[string]bool{
		``: false,
		`{"schemaVersion":2,"rules":{"planner":{"inPlace":""}}}`:      false,
		`{"schemaVersion":2,"rules":{"planner":{"inPlace":"off"}}}`:   true,
		`{"schemaVersion":2,"rules":{"planner":{"inPlace":"maybe"}}}`: true, // invalid locks off
	} {
		if got := decide(t, managed).InPlaceLocked; got != want {
			t.Errorf("InPlaceLocked(%q) = %v, want %v", managed, got, want)
		}
	}
}

// project "" with the built-in default is auto; an explicit project value is the
// project's own choice (an empty one is not).
func TestResolvePlanningMode_Table(t *testing.T) {
	lockAlways := Outcome{Mode: domain.PlanningAlways}
	cases := []struct {
		name    string
		lock    Outcome
		project domain.PlanningMode
		app     domain.PlanningMode
		want    domain.PlanningMode
	}{
		{"managed lock beats project and app", lockAlways, domain.PlanningOff, domain.PlanningOff, domain.PlanningAlways},
		{"project beats app default", Outcome{}, domain.PlanningOff, domain.PlanningAlways, domain.PlanningOff},
		{"explicit auto beats a non-auto app default", Outcome{}, domain.PlanningAuto, domain.PlanningAlways, domain.PlanningAuto},
		{"unset project uses the app default", Outcome{}, "", domain.PlanningAlways, domain.PlanningAlways},
		{"nothing set is auto", Outcome{}, "", "", domain.PlanningAuto},
	}
	for _, c := range cases {
		got := ResolvePlanningMode(c.lock, domain.ProjectConfig{PlanningMode: c.project}, domain.WorkflowDefaults{PlanningMode: c.app})
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// The user layer can no longer set mode or autoApprove: a stored value is
// ignored and a write is refused; only managed locks.
func TestPlannerPolicy_ManagedOnly(t *testing.T) {
	ctx := context.Background()
	store := &ruletest.MemStore{}
	_ = store.SetAgentPolicy(ctx, []byte(`{"version":2,"rules":{"planner":{"mode":"always","autoApprove":"off","inPlace":"off"}}}`), time.Time{})
	e, err := agentpolicy.New([]agentpolicy.Rule{Rule{}}, store)
	if err != nil {
		t.Fatal(err)
	}
	o := Of(e.DecideStatic(ctx))
	if o.Mode != "" || o.AutoApproveLocked {
		t.Fatalf("stale user values applied: %+v", o)
	}
	if !o.InPlaceLocked {
		t.Fatal("inPlace is not managed-only; its user lock must still apply")
	}
	for _, f := range []string{FieldMode, FieldAutoApprove} {
		val := json.RawMessage(`"off"`)
		if f == FieldMode {
			val = json.RawMessage(`"always"`)
		}
		err := e.Update(ctx, "", agentpolicy.Update{Name: {f: val}}, time.Now())
		var ve *agentpolicy.ValidationError
		if !errors.As(err, &ve) {
			t.Errorf("write of %s = %v, want a validation refusal", f, err)
		}
	}
}

// A managed value still locks.
func TestPlannerPolicy_ManagedValueLocks(t *testing.T) {
	o := decide(t, `{"schemaVersion":2,"rules":{"planner":{"autoApprove":"off"}}}`)
	if !o.AutoApproveLocked {
		t.Fatal("managed autoApprove off did not lock")
	}
}
