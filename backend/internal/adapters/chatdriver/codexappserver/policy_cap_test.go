package codexappserver

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestApprovalSettings_CappedByPolicy(t *testing.T) {
	if _, sandbox := approvalSettings(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions); sandbox != "danger-full-access" {
		t.Fatalf("no policy must keep full access, got %q", sandbox)
	}
	for _, level := range []permissioncap.Level{permissioncap.Auto, permissioncap.AcceptEdits} {
		d := policytest.Cap(t, level)
		for _, mode := range []ports.PermissionMode{ports.PermissionModeBypassPermissions, ports.PermissionModeDefault, "unknown"} {
			policy, sandbox := approvalSettings(d, mode)
			if sandbox != "workspace-write" || policy != "on-request" {
				t.Errorf("cap %s mode %q = %s/%s, want on-request/workspace-write", level, mode, policy, sandbox)
			}
		}
	}
}

func TestApprovalReviewer_CappedByPolicy(t *testing.T) {
	if got := approvalReviewer(agentpolicy.Decision{}, ports.PermissionModeAuto); got != "auto_review" {
		t.Fatalf("no policy must keep auto_review, got %q", got)
	}
	if got := approvalReviewer(policytest.Cap(t, permissioncap.Auto), ports.PermissionModeAuto); got != "auto_review" {
		t.Fatalf("auto cap still allows auto, got %q", got)
	}
	d := policytest.Cap(t, permissioncap.AcceptEdits)
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions, ports.PermissionModeDefault} {
		if got := approvalReviewer(d, mode); got != "user" {
			t.Errorf("accept_edits cap mode %q reviewer = %q, want user", mode, got)
		}
	}
}
