package agentpolicy_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
)

// toyRule is a second rule with a bool and a list field, to prove the engine,
// sources, view and record handle a rule it has never heard of.
type toyRule struct {
	badSchema bool
	// rejects makes Validate refuse the named field value pair, to exercise the
	// engine's inconsistent-settings fallback.
	rejectEnabled bool
}

type toyOutcome struct{ on bool }

func (o toyOutcome) Record() map[string]any { return map[string]any{"on": o.on} }

func (toyRule) Name() string { return "toyRule" }
func (r toyRule) Schema() agentpolicy.Schema {
	f := agentpolicy.Field{Name: "enabled", Kind: agentpolicy.KindBool, Default: json.RawMessage(`false`), LegacyKey: "toyEnabled", ProjectOverride: agentpolicy.ProjectAny}
	if r.badSchema {
		f.ProjectOverride = agentpolicy.ProjectTightenOnly // needs an enum
	}
	return agentpolicy.Schema{TitleKey: "toy.title", Fields: []agentpolicy.Field{f,
		{Name: "names", Kind: agentpolicy.KindStringList, Default: json.RawMessage(`[]`), VisibleWhen: &agentpolicy.Condition{Field: "enabled", Equals: "true"}}}}
}
func (r toyRule) Validate(s agentpolicy.Settings) error {
	if r.rejectEnabled && s.Bool("enabled") && len(s.Strings("names")) > 0 {
		return errors.New("enabled and names cannot be combined")
	}
	return nil
}
func (toyRule) Decide(_ agentpolicy.Input, s agentpolicy.Settings) agentpolicy.Outcome {
	return toyOutcome{on: s.Bool("enabled")}
}

func TestANewRuleNeedsNoEnginePlumbing(t *testing.T) {
	h := newHarness(t)
	h.env["TALLPINE_AGENT_TOY_RULE_NAMES"] = "A, B_*"
	h.managed = `{"schemaVersion":2,"rules":{"toyRule":{"enabled":true}}}`
	e, err := agentpolicy.New([]agentpolicy.Rule{toyRule{}}, h.store, agentpolicyManaged(t, h.managed),
		agentpolicy.WithEnv(func(k string) (string, bool) { v, ok := h.env[k]; return v, ok }))
	if err != nil {
		t.Fatal(err)
	}
	v := e.Describe(ctx, "")
	enabled, names := field(t, v, "toyRule", "enabled"), field(t, v, "toyRule", "names")
	if string(enabled.Value) != "true" || enabled.Source != agentpolicy.SourceManaged || !enabled.Locked {
		t.Fatalf("managed v2 value for an unknown-to-engine rule: %+v", enabled)
	}
	if string(names.Value) != `["A","B_*"]` || names.Source != agentpolicy.SourceEnv || names.VisibleWhen == nil {
		t.Fatalf("env value from the derived variable name and view metadata: %+v", names)
	}
	d := e.Decide(ctx, agentpolicy.SessionRef{})
	if out, ok := d.Outcome("toyRule"); !ok || !out.(toyOutcome).on {
		t.Fatal("the decision must carry the rule's outcome")
	}
	if rec := string(d.Record(nil).JSON()); !strings.Contains(rec, `"toyRule"`) || !strings.Contains(rec, `"on":true`) {
		t.Fatalf("record: %s", rec)
	}
	h2 := newHarness(t)
	e2, _ := agentpolicy.New([]agentpolicy.Rule{toyRule{}}, h2.store, agentpolicy.WithManagedFile("", nil), agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err := e2.Update(ctx, "p", agentpolicy.Update{"toyRule": {"enabled": json.RawMessage(`true`)}}, time.Now()); err != nil {
		t.Fatalf("project override of an Any field: %v", err)
	}
	if got := field(t, e2.Describe(ctx, "p"), "toyRule", "enabled"); got.Source != agentpolicy.SourceProject || string(got.Value) != "true" {
		t.Fatalf("%+v", got)
	}
	if err := e2.Update(ctx, "p", agentpolicy.Update{"toyRule": {"names": json.RawMessage(`["X"]`)}}, time.Now()); err == nil {
		t.Fatal("a field with no project override mode must refuse a project write")
	}
}

func TestBrokenSchemaIsRefusedAtStartup(t *testing.T) {
	if _, err := agentpolicy.New([]agentpolicy.Rule{toyRule{badSchema: true}}, nil); err == nil {
		t.Fatal("tightenOnly on a non-enum field must be refused at startup")
	}
}

func agentpolicyManaged(t *testing.T, body string) agentpolicy.Option {
	t.Helper()
	path := t.TempDir() + "/policy.json"
	if err := osWriteFile(path, body); err != nil {
		t.Fatal(err)
	}
	return agentpolicy.WithManagedFile(path, osReadFile)
}

func TestAStoredOverrideOfANeverOverridableFieldIsIgnoredWithAWarning(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"projects":{"p":{"toyRule":{"names":["X"]}}}}`)
	e, err := agentpolicy.New([]agentpolicy.Rule{toyRule{}}, h.store, agentpolicy.WithManagedFile("", nil), agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	v := e.Describe(ctx, "p")
	if got := field(t, v, "toyRule", "names"); string(got.Value) != "[]" || got.Source != agentpolicy.SourceDefault {
		t.Fatalf("a never-overridable field must ignore the project's stored value: %+v", got)
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "not project-overridable") {
		t.Fatalf("ignored override must warn: %v", v.Warnings)
	}
}

// Settings that pass on write (user layer alone) can be inconsistent once combined
// with a managed value. The fallback must keep the administrator's managed value
// and drop only the user's, never reset the rule to defaults.
func TestInconsistentCombinationKeepsTheManagedValue(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"toyRule":{"names":["A"]}}}`)
	h.managed = `{"schemaVersion":2,"rules":{"toyRule":{"enabled":true}}}`
	e, err := agentpolicy.New([]agentpolicy.Rule{toyRule{rejectEnabled: true}}, h.store, agentpolicyManaged(t, h.managed),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	v := e.Describe(ctx, "")
	enabled, names := field(t, v, "toyRule", "enabled"), field(t, v, "toyRule", "names")
	if string(enabled.Value) != "true" || enabled.Source != agentpolicy.SourceManaged || !enabled.Locked {
		t.Fatalf("the managed value must survive an inconsistent combination: %+v", enabled)
	}
	if string(names.Value) != "[]" {
		t.Fatalf("the user's conflicting value is dropped: %+v", names)
	}
	if len(v.Warnings) == 0 || !strings.Contains(strings.Join(v.Warnings, " "), "inconsistent") {
		t.Fatalf("the fallback must be reported: %v", v.Warnings)
	}
}

func TestInconsistentEnvAndManagedAreHeldToTheStrictestValue(t *testing.T) {
	// Even managed + env alone can be inconsistent; the fields keep the layers' values
	// rather than silently resetting to the no-restriction defaults.
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"rules":{"toyRule":{"enabled":true}}}`
	e, err := agentpolicy.New([]agentpolicy.Rule{toyRule{rejectEnabled: true}}, h.store, agentpolicyManaged(t, h.managed),
		agentpolicy.WithEnv(func(k string) (string, bool) {
			if k == "TALLPINE_AGENT_TOY_RULE_NAMES" {
				return "A", true
			}
			return "", false
		}))
	if err != nil {
		t.Fatal(err)
	}
	enabled := field(t, e.Describe(ctx, ""), "toyRule", "enabled")
	if string(enabled.Value) != "true" || enabled.Source != agentpolicy.SourceManaged {
		t.Fatalf("a locked value must never be reset by the fallback: %+v", enabled)
	}
}

func TestUpdateRefusesAValueInconsistentWithAManagedOne(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"rules":{"toyRule":{"enabled":true}}}`
	e, err := agentpolicy.New([]agentpolicy.Rule{toyRule{rejectEnabled: true}}, h.store, agentpolicyManaged(t, h.managed),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	var invalid *agentpolicy.ValidationError
	err = e.Update(ctx, "", agentpolicy.Update{"toyRule": {"names": json.RawMessage(`["A"]`)}}, time.Now())
	if !errors.As(err, &invalid) || len(h.store.Data) != 0 {
		t.Fatalf("a write that conflicts with the managed value must be refused: %v (stored %s)", err, h.store.Data)
	}
}
