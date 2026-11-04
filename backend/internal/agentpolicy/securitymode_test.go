package agentpolicy_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/securitymode"
)

const userOn = `{"version":2,"rules":{"securityMode":{"enabled":true}}}`

func sm(t *testing.T, h *harness, project string) agentpolicy.FieldView {
	return field(t, h.engine().Describe(ctx, project), "securityMode", "enabled")
}

func floors(t *testing.T, h *harness, project string) (ps, ef, gc agentpolicy.FieldView) {
	v := h.engine().Describe(ctx, project)
	return field(t, v, "protectState", "mode"), field(t, v, "envFilter", "mode"), field(t, v, "githubCreds", "mode")
}

func TestSecurityMode_OffByDefaultRestrictsNothing(t *testing.T) {
	h := newHarness(t)
	if f := sm(t, h, ""); string(f.Value) != "false" {
		t.Fatalf("default: %+v", f)
	}
	ps, ef, gc := floors(t, h, "")
	if string(ps.Value) != `"off"` || string(ef.Value) != `"inherit"` || string(gc.Value) != `"env"` {
		t.Fatalf("floors applied while off: %s %s %s", ps.Value, ef.Value, gc.Value)
	}
}

func TestSecurityMode_OffByDefaultNoFloors(t *testing.T) {
	h := newHarness(t)
	if f := sm(t, h, ""); string(f.Value) != "false" || f.Source != agentpolicy.SourceDefault {
		t.Fatalf("default: %+v", f)
	}
	for _, f := range []agentpolicy.FieldView{field(t, h.engine().Describe(ctx, ""), "protectState", "mode"), field(t, h.engine().Describe(ctx, ""), "envFilter", "mode"), field(t, h.engine().Describe(ctx, ""), "githubCreds", "mode")} {
		if f.Source == agentpolicy.SourceSecurityMode {
			t.Fatalf("floor applied by default: %+v", f)
		}
	}
}

func TestSecurityMode_OnRaisesFloorsWithSource(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	ps, ef, gc := floors(t, h, "")
	for _, f := range []agentpolicy.FieldView{ps, ef, gc} {
		if f.Source != agentpolicy.SourceSecurityMode {
			t.Fatalf("source: %+v", f)
		}
	}
	if string(ps.Value) != `"on"` || string(ef.Value) != `"allowlist"` || string(gc.Value) != `"helper"` {
		t.Fatalf("floors: %s %s %s", ps.Value, ef.Value, gc.Value)
	}
	v := h.engine().Describe(ctx, "")
	if !v.SecurityMode.Enabled || v.SecurityMode.Source != agentpolicy.SourceUser || v.SecurityMode.Locked {
		t.Fatalf("view: %+v", v.SecurityMode)
	}
	// Untouched rules.
	for _, rf := range [][2]string{{"permissionCap", "max"}, {"orchestratorApprovals", "enabled"}, {"crossProjectOrchestration", "enabled"}} {
		if f := field(t, v, rf[0], rf[1]); f.Source != agentpolicy.SourceDefault {
			t.Fatalf("%s.%s changed: %+v", rf[0], rf[1], f)
		}
	}
}

func TestSecurityMode_StricterKeptLooserRaised(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"securityMode":{"enabled":true},"githubCreds":{"mode":"none"},"envFilter":{"mode":"inherit"}}}`)
	_, ef, gc := floors(t, h, "")
	if string(gc.Value) != `"none"` || gc.Source != agentpolicy.SourceUser {
		t.Fatalf("stricter must stay: %+v", gc)
	}
	if string(ef.Value) != `"allowlist"` || ef.Source != agentpolicy.SourceSecurityMode {
		t.Fatalf("looser must be raised: %+v", ef)
	}
}

func TestSecurityMode_ProjectOffBeatsUserOn(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"securityMode":{"enabled":true}},"projects":{"p":{"securityMode":{"enabled":false}}}}`)
	if f := sm(t, h, "p"); string(f.Value) != "false" || f.Source != agentpolicy.SourceProject {
		t.Fatalf("%+v", f)
	}
	if ps, _, _ := floors(t, h, "p"); string(ps.Value) != `"off"` {
		t.Fatalf("project opt-out still fenced: %+v", ps)
	}
	if ps, _, _ := floors(t, h, ""); string(ps.Value) != `"on"` {
		t.Fatalf("install unaffected: %+v", ps)
	}
}

func TestSecurityMode_ProjectOnWhenInstallOff(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"projects":{"p":{"securityMode":{"enabled":true}}}}`)
	if ps, _, _ := floors(t, h, "p"); string(ps.Value) != `"on"` {
		t.Fatalf("%+v", ps)
	}
}

func TestSecurityMode_ManagedFalseLocksOff(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	h.managed = `{"schemaVersion":2,"rules":{"securityMode":{"enabled":false}}}`
	f := sm(t, h, "")
	if string(f.Value) != "false" || !f.Locked || f.Source != agentpolicy.SourceManaged {
		t.Fatalf("%+v", f)
	}
	if ps, _, _ := floors(t, h, ""); string(ps.Value) != `"off"` {
		t.Fatalf("%+v", ps)
	}
}

func TestSecurityMode_EnvLocks(t *testing.T) {
	h := newHarness(t)
	h.env["TALLPINE_AGENT_SECURITY_MODE"] = "on"
	f := sm(t, h, "")
	if string(f.Value) != "true" || !f.Locked || f.Source != agentpolicy.SourceEnv {
		t.Fatalf("%+v", f)
	}
	if ps, _, _ := floors(t, h, ""); ps.Source != agentpolicy.SourceSecurityMode {
		t.Fatalf("%+v", ps)
	}
}

func TestSecurityMode_ManagedExplicitPerRuleValueWinsOverFloor(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	h.managed = `{"schemaVersion":2,"rules":{"envFilter":{"mode":"inherit"}}}`
	_, ef, _ := floors(t, h, "")
	if string(ef.Value) != `"inherit"` || ef.Source != agentpolicy.SourceManaged {
		t.Fatalf("%+v", ef)
	}
}

func TestSecurityMode_HardenedImpliesOnAndKeepsProfileSource(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"profile":"hardened"}`
	f := sm(t, h, "")
	if string(f.Value) != "true" || f.Source != agentpolicy.SourceManagedProfile || !f.Locked {
		t.Fatalf("%+v", f)
	}
	ps, ef, gc := floors(t, h, "")
	for _, x := range []agentpolicy.FieldView{ps, ef, gc} {
		if x.Source != agentpolicy.SourceManagedProfile {
			t.Fatalf("hardened floors keep managed_profile: %+v", x)
		}
	}
}

func TestSecurityMode_InvalidManagedValueTurnsOnWithWarning(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"rules":{"securityMode":{"enabled":"maybe"}}}`
	v := h.engine().Describe(ctx, "")
	if !v.SecurityMode.Enabled {
		t.Fatalf("invalid must turn on: %+v", v.SecurityMode)
	}
	if !strings.Contains(strings.Join(v.Warnings, "|"), "securityMode.enabled") {
		t.Fatalf("warnings: %v", v.Warnings)
	}
}

func TestSecurityMode_DecisionOutcomeAndGap(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	d := h.engine().Decide(ctx, agentpolicy.SessionRef{})
	if !securitymode.Of(d).On() || !protectstate.Of(d).Enabled() {
		t.Fatal("mode on must enable the fence")
	}
	out := protectstate.Of(d)
	if out.Required() {
		t.Fatal("Security mode must never make the fence required (only hardened)")
	}
	if err := out.Admit(false, "codex", "darwin"); err != nil {
		t.Fatalf("no refusal outside hardened: %v", err)
	}
	if gap := out.Gap(false, "codex", "darwin"); !strings.Contains(gap, "codex") {
		t.Fatalf("gap: %q", gap)
	}
	if out.Gap(true, "claude", "darwin") != "" {
		t.Fatal("fenced session has no gap")
	}
	if protectstate.Of(h2(t).engine().Decide(ctx, agentpolicy.SessionRef{})).Gap(false, "codex", "linux") != "" {
		t.Fatal("mode off has no gap")
	}
}

func TestSecurityMode_HardenedStillRequiresFence(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"profile":"hardened"}`
	out := protectstate.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{}))
	if !agentpolicy.IsFenceRequired(out.Admit(false, "codex", "darwin")) {
		t.Fatal("hardened must refuse an unfenceable launch")
	}
	if out.Gap(false, "codex", "darwin") != "" {
		t.Fatal("a refused launch has no gap")
	}
}

func TestSecurityMode_AllowDockerOnlyWhenSet(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	if securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{})).AllowDocker() {
		t.Fatal("docker must default closed")
	}
	h.setUser(`{"version":2,"rules":{"securityMode":{"enabled":true,"allowDocker":true}},"projects":{"p":{"securityMode":{"allowDocker":true}}}}`)
	if securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "other"})).AllowDocker() {
		t.Fatal("an install-wide allowDocker must not open Docker for every project")
	}
	if !securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})).AllowDocker() {
		t.Fatal("allowDocker not honoured")
	}
}

func TestUpdate_NullResetsLayerToInherit(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	if err := h.engine().Update(ctx, "", agentpolicy.Update{"securityMode": {"enabled": []byte("null")}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f := sm(t, h, ""); string(f.Value) != "false" || f.Source != agentpolicy.SourceDefault {
		t.Fatalf("%+v", f)
	}
}

func h2(t *testing.T) *harness { return newHarness(t) }

func TestSecurityMode_AllowDockerRefusedInstallWide(t *testing.T) {
	h := newHarness(t)
	err := h.engine().Update(ctx, "", agentpolicy.Update{"securityMode": {"allowDocker": []byte("true")}}, time.Now())
	var ve *agentpolicy.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("install-wide allowDocker write = %v, want a validation error", err)
	}
	if err := h.engine().Update(ctx, "p", agentpolicy.Update{"securityMode": {"allowDocker": []byte("true")}}, time.Now()); err != nil {
		t.Fatalf("project write refused: %v", err)
	}
}

func TestSecurityMode_HardenedLocksEvenWhenUserHasItOn(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	h.managed = `{"schemaVersion":2,"profile":"hardened"}`
	if f := sm(t, h, ""); !f.Locked || f.Source != agentpolicy.SourceManagedProfile {
		t.Fatalf("%+v", f)
	}
	err := h.engine().Update(ctx, "", agentpolicy.Update{"securityMode": {"enabled": []byte("false")}}, time.Now())
	var le *agentpolicy.LockedError
	if !errors.As(err, &le) {
		t.Fatalf("write of false under hardened = %v, want LockedError", err)
	}
}

func TestSecurityMode_HardenedIgnoresProjectAllowDocker(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"projects":{"p":{"securityMode":{"allowDocker":true}}}}`)
	if !securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})).AllowDocker() {
		t.Fatal("precondition: project value honoured without the profile")
	}
	h.managed = `{"schemaVersion":2,"profile":"hardened"}`
	if securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})).AllowDocker() {
		t.Fatal("a project value must not open Docker under the hardened profile")
	}
	h.managed = `{"schemaVersion":2,"profile":"hardened","rules":{"securityMode":{"allowDocker":true}}}`
	if !securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{Project: "p"})).AllowDocker() {
		t.Fatal("a managed allowDocker must still apply")
	}
}

const hardenedDoc = `{"schemaVersion":2,"profile":"hardened"}`

func TestSecurityMode_HardenedOverridesLockedOff(t *testing.T) {
	for name, set := range map[string]func(*harness){
		"managed": func(h *harness) {
			h.managed = `{"schemaVersion":2,"profile":"hardened","rules":{"securityMode":{"enabled":false}}}`
		},
		"env": func(h *harness) {
			h.managed = hardenedDoc
			h.env["TALLPINE_AGENT_SECURITY_MODE"] = "off"
		},
	} {
		h := newHarness(t)
		set(h)
		v := h.engine().Describe(ctx, "")
		if !v.SecurityMode.Enabled || v.SecurityMode.Source != agentpolicy.SourceManagedProfile || !v.SecurityMode.Locked {
			t.Errorf("%s: view %+v", name, v.SecurityMode)
		}
		if !securitymode.Of(h.engine().Decide(ctx, agentpolicy.SessionRef{})).On() {
			t.Errorf("%s: outcome off under hardened", name)
		}
	}
}

func TestSecurityMode_DevModeLiftsFloors(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"securityMode":{"enabled":true},"devMode":{"enabled":true}}}`)
	ps, ef, gc := floors(t, h, "")
	if string(ps.Value) != `"off"` || string(ef.Value) != `"inherit"` || string(gc.Value) != `"env"` {
		t.Fatalf("Dev mode must leave the fence off: %s %s %s", ps.Value, ef.Value, gc.Value)
	}
	if !h.engine().Describe(ctx, "").SecurityMode.Enabled {
		t.Fatal("the switch itself still reads on; only the floors are lifted")
	}
	// Dev mode off again: the floors come back with no relaunch of the daemon.
	h.setUser(userOn)
	if ps, _, _ := floors(t, h, ""); string(ps.Value) != `"on"` {
		t.Fatalf("floors not restored: %s", ps.Value)
	}
}

func upd(t *testing.T, h *harness, project, rule, field, val string) error {
	t.Helper()
	return h.engine().Update(ctx, project, agentpolicy.Update{rule: {field: []byte(val)}}, time.Now())
}

func TestUpdate_ModeConflictRefusedBothWays(t *testing.T) {
	h := newHarness(t)
	h.setUser(userOn)
	if err := upd(t, h, "", "devMode", "enabled", "true"); !errors.Is(err, agentpolicy.ErrModeConflict) {
		t.Fatalf("dev on while security on = %v", err)
	}
	h = newHarness(t)
	h.setUser(`{"version":2,"rules":{"devMode":{"enabled":true}}}`)
	if err := upd(t, h, "", "securityMode", "enabled", "true"); !errors.Is(err, agentpolicy.ErrModeConflict) {
		t.Fatalf("security on while dev on = %v", err)
	}
	// Turning one off while writing the other on is the Unrestricted switch.
	if err := h.engine().Update(ctx, "", agentpolicy.Update{"devMode": {"enabled": []byte("true")}, "securityMode": {"enabled": []byte("null")}}, time.Now()); err != nil {
		t.Fatalf("mode swap refused: %v", err)
	}
}

func TestUpdate_ModeConflictManagedSecurityOn(t *testing.T) {
	h := newHarness(t)
	h.managed = `{"schemaVersion":2,"rules":{"securityMode":{"enabled":true}}}`
	if err := upd(t, h, "", "devMode", "enabled", "true"); !errors.Is(err, agentpolicy.ErrModeConflict) {
		t.Fatalf("dev on under managed security on = %v", err)
	}
}

func TestUpdate_UnrelatedWriteSucceedsWithStoredBothOn(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"securityMode":{"enabled":true},"devMode":{"enabled":true}}}`)
	if err := upd(t, h, "", "protectState", "mode", `"on"`); err != nil {
		t.Fatalf("unrelated write refused: %v", err)
	}
}

func TestUpdate_ProjectSecurityWriteAcceptedWhileDevOn(t *testing.T) {
	h := newHarness(t)
	h.setUser(`{"version":2,"rules":{"devMode":{"enabled":true}}}`)
	if err := upd(t, h, "p", "securityMode", "enabled", "true"); err != nil {
		t.Fatalf("project write refused: %v", err)
	}
}
