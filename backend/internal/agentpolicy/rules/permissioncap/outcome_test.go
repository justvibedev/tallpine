package permissioncap_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestOf_ZeroDecisionRestrictsNothing(t *testing.T) {
	o := permissioncap.Of(agentpolicy.Decision{})
	if o.ForbidsBypass() || o.Level() != permissioncap.None {
		t.Fatalf("zero decision must be uncapped: %v", o.Level())
	}
}

func TestOutcomeEffects(t *testing.T) {
	d := policytest.Cap(t, permissioncap.Auto)
	o := permissioncap.Of(d)
	if !o.ForbidsBypass() || o.PinnedCodexSandbox() != "workspace-write" || !o.ExplicitClaudeDefault() {
		t.Fatalf("auto cap effects: %+v", o)
	}
	if got := o.Lower(domain.HarnessClaudeCode, domain.PermissionModeBypassPermissions); got != domain.PermissionModeAuto {
		t.Fatalf("lowered = %q", got)
	}
	if none := permissioncap.Of(policytest.Cap(t, permissioncap.None)); none.PinnedCodexSandbox() != "" || none.ExplicitClaudeDefault() {
		t.Fatal("no cap must add no sandbox flag or explicit default")
	}
}

// A decision made before a managed tightening still sees it through the live
// read, but not through the frozen level: per-turn seams use LowerLive.
func TestLowerLive_FollowsALaterTightening(t *testing.T) {
	engine, store := policytest.Engine(t, map[string]map[string]any{permissioncap.Name: {permissioncap.FieldMax: "none"}})
	ctx := context.Background()
	d := engine.Decide(ctx, agentpolicy.SessionRef{Project: "p", Harness: domain.HarnessClaudeCode})
	o := permissioncap.Of(d)
	bypass := domain.PermissionModeBypassPermissions
	if o.LowerLive(domain.HarnessClaudeCode, bypass) != bypass {
		t.Fatal("uncapped must keep bypass")
	}
	if err := engine.Update(ctx, "", agentpolicy.Update{permissioncap.Name: {permissioncap.FieldMax: []byte(`"accept_edits"`)}}, timeNow()); err != nil {
		t.Fatal(err)
	}
	_ = store
	if got := o.Lower(domain.HarnessClaudeCode, bypass); got != bypass {
		t.Fatalf("the frozen decision must not change, got %q", got)
	}
	if got := permissioncap.Of(d).LowerLive(domain.HarnessClaudeCode, bypass); got != domain.PermissionModeAcceptEdits {
		t.Fatalf("live tightening not applied, got %q", got)
	}
}

func TestClaudeMode_LiveCapAndExplicitDefault(t *testing.T) {
	engine, _ := policytest.Engine(t, map[string]map[string]any{permissioncap.Name: {permissioncap.FieldMax: "none"}})
	ctx := context.Background()
	o := permissioncap.Of(engine.Decide(ctx, agentpolicy.SessionRef{Project: "p", Harness: domain.HarnessClaudeCode}))
	bypass := domain.PermissionModeBypassPermissions
	for _, live := range []bool{false, true} {
		if got := o.ClaudeMode(bypass, live); got != "bypassPermissions" {
			t.Fatalf("uncapped live=%v: got %q", live, got)
		}
		if got := o.ClaudeMode(domain.PermissionModeDefault, live); got != "" {
			t.Fatalf("uncapped default live=%v: got %q", live, got)
		}
	}
	if err := engine.Update(ctx, "", agentpolicy.Update{permissioncap.Name: {permissioncap.FieldMax: []byte(`"accept_edits"`)}}, timeNow()); err != nil {
		t.Fatal(err)
	}
	if got := o.ClaudeMode(bypass, false); got != "bypassPermissions" {
		t.Fatalf("frozen cap must ignore the later push, got %q", got)
	}
	if got := o.ClaudeMode(bypass, true); got != "acceptEdits" {
		t.Fatalf("live cap: got %q", got)
	}
	if got := o.ClaudeMode(domain.PermissionModeDefault, true); got != "default" {
		t.Fatalf("live cap must name default explicitly, got %q", got)
	}
}

func timeNow() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) }
