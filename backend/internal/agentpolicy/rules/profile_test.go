package rules_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/envfilter"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
)

var hardenedFloor = map[string]string{
	"permissionCap.max":                 `"auto"`,
	"envFilter.mode":                    `"allowlist"`,
	"githubCreds.mode":                  `"helper"`,
	"protectState.mode":                 `"on"`,
	"repoAgentConfig.enabled":           `false`,
	"orchestratorApprovals.enabled":     `false`,
	"crossProjectOrchestration.enabled": `false`,
	"devMode.enabled":                   `false`,
}

func fieldState(view agentpolicy.View, key string) (agentpolicy.FieldView, bool) {
	for _, r := range view.Rules {
		for _, f := range r.Fields {
			if r.Name+"."+f.Name == key {
				return f, true
			}
		}
	}
	return agentpolicy.FieldView{}, false
}

// Every floor names a real field of a registered rule, so a renamed field
// cannot silently drop out of the profile.
func TestHardenedFloorsNameRealFields(t *testing.T) {
	engine, _ := policytest.Engine(t, nil)
	view := engine.Describe(context.Background(), "")
	got := agentpolicy.HardenedFloorFields()
	if len(got) != len(hardenedFloor) {
		t.Fatalf("floor fields = %v", got)
	}
	for _, key := range got {
		if _, ok := fieldState(view, key); !ok {
			t.Fatalf("floor %s names no registered field", key)
		}
		if _, ok := hardenedFloor[key]; !ok {
			t.Fatalf("unexpected floor %s", key)
		}
	}
}

func TestManagedProfileParse(t *testing.T) {
	cases := []struct {
		name, doc   string
		profile     string
		warn        string
		overrides   map[string]string // key -> expected value and source managed
		noFloorKeys bool
	}{
		{name: "absent", doc: `{"schemaVersion":2,"rules":{}}`, noFloorKeys: true},
		{name: "hardened", doc: `{"schemaVersion":2,"profile":"hardened"}`, profile: "hardened"},
		{name: "typo", doc: `{"schemaVersion":2,"profile":"Hardned"}`, profile: "hardened", warn: "unknown"},
		{name: "unknown name", doc: `{"schemaVersion":2,"profile":"strict"}`, profile: "hardened", warn: "unknown"},
		{name: "malformed", doc: `{"schemaVersion":2,"profile":1}`, profile: "hardened", warn: "invalid"},
		{name: "v1 flat", doc: `{"maxPermission":"none","profile":"hardened"}`, profile: "hardened", warn: "looser"},
		{name: "v2 explicit stricter", doc: `{"schemaVersion":2,"profile":"hardened","rules":{"permissionCap":{"max":"accept_edits"},"githubCreds":{"mode":"none"}}}`,
			profile: "hardened", overrides: map[string]string{"permissionCap.max": `"accept_edits"`, "githubCreds.mode": `"none"`}},
		{name: "v2 explicit looser", doc: `{"schemaVersion":2,"profile":"hardened","rules":{"githubCreds":{"mode":"env"},"repoAgentConfig":{"enabled":true},"protectState":{"mode":"off"}}}`,
			profile: "hardened", warn: "looser"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, _ := policytest.Managed(t, tc.doc, nil)
			ctx := context.Background()
			if got := engine.ManagedProfile(); got != tc.profile {
				t.Fatalf("ManagedProfile = %q, want %q", got, tc.profile)
			}
			d := engine.DecideStatic(ctx)
			if d.Profile() != tc.profile {
				t.Fatalf("Decision.Profile = %q", d.Profile())
			}
			view := engine.Describe(ctx, "")
			if view.Profile != tc.profile {
				t.Fatalf("View.Profile = %q", view.Profile)
			}
			if tc.warn != "" && !strings.Contains(strings.Join(view.Warnings, "|"), tc.warn) {
				t.Fatalf("warnings %v lack %q", view.Warnings, tc.warn)
			}
			for key, floor := range hardenedFloor {
				f, _ := fieldState(view, key)
				if tc.noFloorKeys {
					if f.Source == agentpolicy.SourceManagedProfile {
						t.Fatalf("%s from profile without one", key)
					}
					continue
				}
				want, wantSrc := floor, agentpolicy.SourceManagedProfile
				if v, ok := tc.overrides[key]; ok {
					want, wantSrc = v, agentpolicy.SourceManaged
				}
				if string(f.Value) != want || f.Source != wantSrc || !f.Locked {
					t.Fatalf("%s = %s (%s, locked %v), want %s (%s, locked)", key, f.Value, f.Source, f.Locked, want, wantSrc)
				}
			}
			if !tc.noFloorKeys && !protectstate.Of(d).Required() {
				t.Fatal("protectState not required under the profile")
			}
		})
	}
}

func TestNoProfileDecisionsUnchanged(t *testing.T) {
	engine, _ := policytest.Managed(t, `{"schemaVersion":2,"rules":{"protectState":{"mode":"on"}}}`, nil)
	d := engine.DecideStatic(context.Background())
	if d.Profile() != "" || protectstate.Of(d).Required() || !protectstate.Of(d).Enabled() {
		t.Fatalf("profile %q required %v", d.Profile(), protectstate.Of(d).Required())
	}
	if _, ok := d.Record(nil)["profile"]; ok {
		t.Fatal("record carries a profile without one")
	}
	// Without the profile an unknown harness keeps the union of credential lists.
	allow, _ := policytest.Managed(t, `{"schemaVersion":2,"rules":{"envFilter":{"mode":"allowlist"}}}`, nil)
	res := envfilter.Of(allow.DecideStatic(context.Background())).ForHarness("mystery").Filter([]string{"OPENAI_API_KEY=x"}, false, nil)
	if len(res.Keep) != 1 {
		t.Fatalf("no-profile union lost: %v", res.Keep)
	}
}

func TestProfileLocksRefuseUpdates(t *testing.T) {
	engine, _ := policytest.Managed(t, `{"schemaVersion":2,"profile":"hardened"}`, nil)
	update := agentpolicy.Update{"githubCreds": {"mode": json.RawMessage(`"env"`)}}
	err := engine.Update(context.Background(), "", update, timeNow())
	var locked *agentpolicy.LockedError
	if !errors.As(err, &locked) || locked.Fields[0] != "githubCreds.mode" {
		t.Fatalf("err = %v, want LockedError", err)
	}
}

func TestProfileEnvAllowlistHasNoUnionFallback(t *testing.T) {
	out := envfilter.Of(policytest.Hardened(t))
	if !out.Allowlist() {
		t.Fatal("allowlist not on")
	}
	for _, harness := range []string{"", "mystery"} {
		if res := out.ForHarness(harness).Filter([]string{"OPENAI_API_KEY=x", "ANTHROPIC_API_KEY=y", "PATH=/bin"}, false, nil); len(res.Keep) != 1 {
			t.Fatalf("harness %q kept %v, want PATH only", harness, res.Keep)
		}
	}
	if res := out.ForHarness("codex").Filter([]string{"OPENAI_API_KEY=x"}, false, nil); len(res.Keep) != 1 {
		t.Fatal("codex lost its own credentials")
	}
}

func TestProtectStateAdmit(t *testing.T) {
	out := protectstate.Of(policytest.Hardened(t))
	if err := out.Admit(true, "claude-code", "darwin"); err != nil {
		t.Fatal(err)
	}
	err := out.Admit(false, "codex", "darwin")
	if !agentpolicy.IsFenceRequired(err) || !errors.Is(err, agentpolicy.ErrRequiresFence) || !strings.Contains(err.Error(), "codex on darwin") {
		t.Fatalf("err = %v", err)
	}
	if err := protectstate.Of(policytest.Decision(t, map[string]map[string]any{"protectState": {"mode": "on"}})).Admit(false, "codex", "linux"); err != nil {
		t.Fatalf("protectState on without the profile refused: %v", err)
	}
}

func timeNow() time.Time { return time.Unix(1_800_000_000, 0) }
