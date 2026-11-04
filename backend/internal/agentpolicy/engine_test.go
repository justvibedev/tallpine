package agentpolicy_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
)

var ctx = context.Background()

type harness struct {
	t       *testing.T
	store   *policytest.Store
	env     map[string]string
	managed string
}

func newHarness(t *testing.T) *harness {
	return &harness{t: t, store: &policytest.Store{}, env: map[string]string{}}
}

func (h *harness) engine() *agentpolicy.Engine {
	h.t.Helper()
	path := ""
	if h.managed != "" {
		path = filepath.Join(h.t.TempDir(), "policy.json")
		if err := os.WriteFile(path, []byte(h.managed), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	e, err := agentpolicy.New(rules.All(), h.store,
		agentpolicy.WithManagedFile(path, os.ReadFile),
		agentpolicy.WithEnv(func(k string) (string, bool) { v, ok := h.env[k]; return v, ok }))
	if err != nil {
		h.t.Fatal(err)
	}
	return e
}

func field(t *testing.T, v agentpolicy.View, rule, name string) agentpolicy.FieldView {
	t.Helper()
	for _, r := range v.Rules {
		if r.Name == rule {
			for _, f := range r.Fields {
				if f.Name == name {
					return f
				}
			}
		}
	}
	t.Fatalf("no field %s.%s", rule, name)
	return agentpolicy.FieldView{}
}

func capOf(t *testing.T, v agentpolicy.View) agentpolicy.FieldView {
	return field(t, v, "permissionCap", "max")
}

func (f *harness) setUser(doc string) { f.store.Data = []byte(doc) }

func TestDefaultsRestrictNothing(t *testing.T) {
	v := newHarness(t).engine().Describe(ctx, "")
	f := capOf(t, v)
	if string(f.Value) != `"none"` || f.Source != agentpolicy.SourceDefault || f.Locked || len(v.Warnings) != 0 {
		t.Fatalf("%+v %v", f, v.Warnings)
	}
}

func TestPrecedence_ManagedEnvProjectUserDefault(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"permissionCap":{"max":"none"}},"projects":{"p":{"permissionCap":{"max":"auto"}}}}`)
	got := func(project string) agentpolicy.FieldView { return capOf(t, h.engine().Describe(ctx, project)) }
	if f := got(""); string(f.Value) != `"none"` || f.Source != agentpolicy.SourceUser {
		t.Fatalf("user: %+v", f)
	}
	if f := got("p"); string(f.Value) != `"auto"` || f.Source != agentpolicy.SourceProject || f.Locked {
		t.Fatalf("project beats user: %+v", f)
	}
	h.env["TALLPINE_AGENT_MAX_PERMISSION"] = "accept_edits"
	if f := got("p"); string(f.Value) != `"accept_edits"` || f.Source != agentpolicy.SourceEnv || !f.Locked {
		t.Fatalf("env beats project (legacy alias): %+v", f)
	}
	delete(h.env, "TALLPINE_AGENT_MAX_PERMISSION")
	h.env["TALLPINE_AGENT_PERMISSION_CAP_MAX"] = "auto"
	h.managed = `{"schemaVersion":2,"rules":{"permissionCap":{"max":"accept_edits"}}}`
	if f := got("p"); string(f.Value) != `"accept_edits"` || f.Source != agentpolicy.SourceManaged || !f.Locked {
		t.Fatalf("managed beats env (derived name): %+v", f)
	}
}

func TestProjectOverride_TightenOnly(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"permissionCap":{"max":"auto"}},"projects":{"p":{"permissionCap":{"max":"none"}}}}`)
	v := h.engine().Describe(ctx, "p")
	if f := capOf(t, v); string(f.Value) != `"auto"` || f.Source != agentpolicy.SourceUser {
		t.Fatalf("a loosening project override must be ignored: %+v", f)
	}
	if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], "loosen") {
		t.Fatalf("ignored override must warn: %v", v.Warnings)
	}
	e := h.engine()
	err := e.Update(ctx, "p", agentpolicy.Update{"permissionCap": {"max": json.RawMessage(`"none"`)}}, time.Now())
	var ve *agentpolicy.ValidationError
	if !errors.As(err, &ve) || !errors.Is(agentpolicy.ErrLoosens, agentpolicy.ErrLoosens) || !strings.Contains(err.Error(), "tighten") {
		t.Fatalf("writing a loosening override must be refused: %v", err)
	}
	if err := e.Update(ctx, "p", agentpolicy.Update{"permissionCap": {"max": json.RawMessage(`"accept_edits"`)}}, time.Now()); err != nil {
		t.Fatalf("a tightening override must be accepted: %v", err)
	}
	if f := capOf(t, h.engine().Describe(ctx, "p")); string(f.Value) != `"accept_edits"` || f.Source != agentpolicy.SourceProject {
		t.Fatalf("%+v", f)
	}
	if f := capOf(t, h.engine().Describe(ctx, "other")); string(f.Value) != `"auto"` {
		t.Fatalf("an override must not leak to other projects: %+v", f)
	}
}

func TestManagedFile_V1FlatAndUnknownKeysSilently(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"maxPermission":"auto","futureKey":1}`
	v := h.engine().Describe(ctx, "")
	if f := capOf(t, v); string(f.Value) != `"auto"` || f.Source != agentpolicy.SourceManaged || len(v.Warnings) != 0 {
		t.Fatalf("v1 flat file: %+v %v", f, v.Warnings)
	}
}

func TestManagedFile_V2UnknownRuleAndNewerVersionWarn(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":3,"rules":{"permissionCap":{"max":"auto"},"futureRule":{"x":1}}}`
	v := h.engine().Describe(ctx, "")
	if f := capOf(t, v); string(f.Value) != `"auto"` {
		t.Fatalf("known settings must still apply: %+v", f)
	}
	joined := strings.Join(v.Warnings, "|")
	if !strings.Contains(joined, "newer") || !strings.Contains(joined, "futureRule") {
		t.Fatalf("newer version and unknown rule must warn: %v", v.Warnings)
	}
}

func TestManagedFile_MalformedAppliesStrictestWithWarning(t *testing.T) {
	for _, body := range []string{`{not json`, `{"maxPermission":"bogus"}`, `{"schemaVersion":2,"rules":{"permissionCap":{"max":"bogus"}}}`, `{"schemaVersion":2,"rules":[1]}`} {
		h := newHarness(t)
		h.managed = body
		v := h.engine().Describe(ctx, "")
		if f := capOf(t, v); string(f.Value) != `"accept_edits"` || !f.Locked || len(v.Warnings) == 0 {
			t.Fatalf("body %q: %+v %v", body, f, v.Warnings)
		}
	}
}

func TestManagedFile_MissingIsNoLayer(t *testing.T) {
	h := newHarness(t)
	e, err := agentpolicy.New(rules.All(), h.store, agentpolicy.WithManagedFile(filepath.Join(t.TempDir(), "absent.json"), os.ReadFile),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	v := e.Describe(ctx, "")
	if f := capOf(t, v); string(f.Value) != `"none"` || len(v.Warnings) != 0 || v.ManagedPath != "" {
		t.Fatalf("%+v %v", f, v)
	}
}

func TestInvalidEnvValueAppliesStrictestWithWarning(t *testing.T) {
	h := newHarness(t)
	h.env["TALLPINE_AGENT_MAX_PERMISSION"] = "accept-edits" // the likely typo: hyphen
	v := h.engine().Describe(ctx, "")
	if f := capOf(t, v); string(f.Value) != `"accept_edits"` || f.Source != agentpolicy.SourceEnv || len(v.Warnings) == 0 {
		t.Fatalf("%+v %v", f, v.Warnings)
	}
	h = newHarness(t)
	h.env["TALLPINE_AGENT_MAX_PERMISSION"] = "auto"
	if v := h.engine().Describe(ctx, ""); len(v.Warnings) != 0 {
		t.Fatalf("a valid value must not warn: %v", v.Warnings)
	}
}

func TestUpdate_RefusesLockedFieldAndMergesTheRest(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"maxPermission":"accept_edits"}`
	e := h.engine()
	err := e.Update(ctx, "", agentpolicy.Update{"permissionCap": {"max": json.RawMessage(`"none"`)}}, time.Now())
	var locked *agentpolicy.LockedError
	if !errors.As(err, &locked) || locked.Fields[0] != "permissionCap.max" || len(h.store.Data) != 0 {
		t.Fatalf("locked write must be refused without storing: %v %s", err, h.store.Data)
	}
	if err := e.Update(ctx, "", agentpolicy.Update{"permissionCap": {"max": json.RawMessage(`"accept_edits"`)}}, time.Now()); err != nil {
		t.Fatalf("writing the locked value itself is a no-op, not a conflict: %v", err)
	}
}

func TestUpdate_ValidationErrors(t *testing.T) {
	e := newHarness(t).engine()
	for name, u := range map[string]agentpolicy.Update{
		"unknown rule":  {"nope": {"max": json.RawMessage(`"auto"`)}},
		"unknown field": {"permissionCap": {"nope": json.RawMessage(`"auto"`)}},
		"bad value":     {"permissionCap": {"max": json.RawMessage(`"accept-edits"`)}},
		"empty":         {},
	} {
		var ve *agentpolicy.ValidationError
		if err := e.Update(ctx, "", u, time.Now()); !errors.As(err, &ve) {
			t.Errorf("%s: want ValidationError, got %v", name, err)
		}
	}
}

func TestUserDoc_V1IsReadAndRewrittenAsV2(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"maxPermission":"auto"}`) // what PR 1 stored
	e := h.engine()
	if f := capOf(t, e.Describe(ctx, "")); string(f.Value) != `"auto"` || f.Source != agentpolicy.SourceUser {
		t.Fatalf("v1 document must still apply: %+v", f)
	}
	if err := e.Update(ctx, "p", agentpolicy.Update{"permissionCap": {"max": json.RawMessage(`"accept_edits"`)}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version int                       `json:"version"`
		Rules   map[string]map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(h.store.Data, &doc); err != nil || doc.Version != 2 || doc.Rules["permissionCap"]["max"] != "auto" {
		t.Fatalf("the rewrite must be v2 and keep the global value: %s", h.store.Data)
	}
}

func TestDecision_ZeroValueAndSessionBinding(t *testing.T) {
	var zero agentpolicy.Decision
	if _, ok := zero.Outcome("permissionCap"); ok || zero.Source("permissionCap", "max") != agentpolicy.SourceDefault || len(zero.Warnings()) != 0 {
		t.Fatal("the zero decision restricts nothing")
	}
	if rec := zero.Record(nil); rec["v"] != agentpolicy.RecordVersion {
		t.Fatalf("%v", rec)
	}
	h := newHarness(t)
	d := h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})
	if bound := d.WithSession("p-7"); bound.Session().ID != "p-7" || d.Session().ID != "" {
		t.Fatal("WithSession must copy, not mutate")
	}
}

func TestDecisionRecord_HasSourcesAndNoValuesOfSecrets(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"maxPermission":"auto"}`
	d := h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})
	rec := d.Record(map[string]map[string]any{"permissionCap": {"requested": "bypass-permissions", "effective": "auto"}})
	encoded := string(rec.JSON())
	for _, want := range []string{`"v":2`, `"permissionCap"`, `"max":"auto"`, `"max":"managed"`, `"requested":"bypass-permissions"`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("record lacks %s: %s", want, encoded)
		}
	}
}

func TestNewRejectsBrokenRules(t *testing.T) {
	if _, err := agentpolicy.New([]agentpolicy.Rule{permissioncap.Rule{}, permissioncap.Rule{}}, nil); err == nil {
		t.Fatal("a rule registered twice must be refused at startup")
	}
}

func osWriteFile(path, body string) error { return os.WriteFile(path, []byte(body), 0o600) }

var osReadFile = os.ReadFile

func TestManagedFile_UnreadableAppliesStrictest(t *testing.T) {
	h := newHarness(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(`{"maxPermission":"auto"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	denied := func(string) ([]byte, error) { return nil, fs.ErrPermission }
	e, err := agentpolicy.New(rules.All(), h.store, agentpolicy.WithManagedFile(path, denied),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	v := e.Describe(ctx, "")
	if f := capOf(t, v); string(f.Value) != `"accept_edits"` || !f.Locked || len(v.Warnings) == 0 {
		t.Fatalf("unreadable policy must fail closed: %+v %v", f, v.Warnings)
	}
}
