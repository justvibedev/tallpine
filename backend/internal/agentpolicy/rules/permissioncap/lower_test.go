package permissioncap

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestLower_Table(t *testing.T) {
	const (
		def = domain.PermissionModeDefault
		ae  = domain.PermissionModeAcceptEdits
		au  = domain.PermissionModeAuto
		by  = domain.PermissionModeBypassPermissions
	)
	harnesses := []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex, domain.HarnessDeepSeek}
	type key struct {
		level Level
		codex bool
		mode  domain.PermissionMode
	}
	want := map[key]domain.PermissionMode{
		{None, false, def}: def, {None, false, ae}: ae, {None, false, au}: au, {None, false, by}: by,
		{None, true, def}: def, {None, true, ae}: ae, {None, true, au}: au, {None, true, by}: by,
		{Auto, false, def}: def, {Auto, false, ae}: ae, {Auto, false, au}: au, {Auto, false, by}: au,
		{Auto, true, def}: au, {Auto, true, ae}: ae, {Auto, true, au}: au, {Auto, true, by}: au,
		{AcceptEdits, false, def}: def, {AcceptEdits, false, ae}: ae, {AcceptEdits, false, au}: ae, {AcceptEdits, false, by}: ae,
		{AcceptEdits, true, def}: ae, {AcceptEdits, true, ae}: ae, {AcceptEdits, true, au}: ae, {AcceptEdits, true, by}: ae,
	}
	for _, h := range harnesses {
		for k, w := range want {
			if k.codex != (h == domain.HarnessCodex) {
				continue
			}
			if got := lower(k.level, h, k.mode); got != w {
				t.Errorf("cap %q harness %s mode %q = %q, want %q", k.level, h, k.mode, got, w)
			}
		}
	}
}

func TestLower_KiroAutoStaysScoped(t *testing.T) {
	// Kiro v3 auto is the scoped dev-shell preset; only bypass is full access.
	if got := lower(Auto, domain.HarnessKiro, domain.PermissionModeAuto); got != domain.PermissionModeAuto {
		t.Errorf("cap auto kiro auto = %q, want auto", got)
	}
	if got := lower(Auto, domain.HarnessKiro, domain.PermissionModeBypassPermissions); got != domain.PermissionModeAuto {
		t.Errorf("cap auto kiro bypass = %q, want auto", got)
	}
	for _, mode := range []domain.PermissionMode{domain.PermissionModeAuto, domain.PermissionModeBypassPermissions} {
		if got := lower(AcceptEdits, domain.HarnessKiro, mode); got != domain.PermissionModeAcceptEdits {
			t.Errorf("cap accept-edits kiro %q = %q", mode, got)
		}
	}
	if got := lower(Auto, domain.HarnessKiro, domain.PermissionModeDefault); got != domain.PermissionModeDefault {
		t.Errorf("kiro default must stay default, got %q", got)
	}
	if got := lower(None, domain.HarnessKiro, domain.PermissionModeAuto); got != domain.PermissionModeAuto {
		t.Errorf("no cap must not touch kiro, got %q", got)
	}
}

func TestLower_UnknownAndEmptyModeOnCodex(t *testing.T) {
	// Codex treats default, empty and unknown as full access.
	for _, m := range []domain.PermissionMode{"", "weird"} {
		if got := lower(Auto, domain.HarnessCodex, m); got != domain.PermissionModeAuto {
			t.Errorf("auto cap, codex, %q = %q", m, got)
		}
		if got := lower(AcceptEdits, domain.HarnessCodex, m); got != domain.PermissionModeAcceptEdits {
			t.Errorf("accept_edits cap, codex, %q = %q", m, got)
		}
		if got := lower(None, domain.HarnessCodex, m); got != m {
			t.Errorf("none cap must not touch %q, got %q", m, got)
		}
	}
}

func TestZeroOutcomeRestrictsNothing(t *testing.T) {
	var o Outcome
	if o.ForbidsBypass() || o.PinnedCodexSandbox() != "" || o.ExplicitClaudeDefault() {
		t.Fatal("zero outcome must restrict nothing")
	}
	if got := o.Lower(domain.HarnessCodex, domain.PermissionModeBypassPermissions); got != domain.PermissionModeBypassPermissions {
		t.Fatalf("zero outcome lowered a mode: %q", got)
	}
}
