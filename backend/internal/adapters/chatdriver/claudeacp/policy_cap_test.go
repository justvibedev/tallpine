package claudeacp

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestClaudeSessionMode_CappedByPolicy(t *testing.T) {
	if got := claudeSessionMode(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions); got != "bypassPermissions" {
		t.Fatalf("no policy must keep bypass, got %q", got)
	}
	cases := map[permissioncap.Level][2]string{
		permissioncap.Auto:        {"auto", "auto"},
		permissioncap.AcceptEdits: {"acceptEdits", "acceptEdits"},
	}
	for level, want := range cases {
		d := policytest.Cap(t, level)
		if got := claudeSessionMode(d, ports.PermissionModeBypassPermissions); got != want[0] {
			t.Errorf("cap %s bypass = %q, want %q", level, got, want[0])
		}
		if got := claudeSessionMode(d, ports.PermissionModeAuto); got != want[1] {
			t.Errorf("cap %s auto = %q, want %q", level, got, want[1])
		}
	}
}

func TestClaudeSessionMode_DefaultExplicitUnderCap(t *testing.T) {
	if got := claudeSessionMode(agentpolicy.Decision{}, ports.PermissionModeDefault); got != "" {
		t.Fatalf("no policy must leave default implicit, got %q", got)
	}
	for _, level := range []permissioncap.Level{permissioncap.Auto, permissioncap.AcceptEdits} {
		if got := claudeSessionMode(policytest.Cap(t, level), ports.PermissionModeDefault); got != "default" {
			t.Errorf("cap %s: default = %q, want explicit default", level, got)
		}
	}
}
