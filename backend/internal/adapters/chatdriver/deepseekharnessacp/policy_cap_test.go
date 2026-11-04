package deepseekharnessacp

import (
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestPermissionPolicy_CappedByPolicy(t *testing.T) {
	edit, execKind := acpsdk.ToolKindEdit, acpsdk.ToolKindExecute
	req := func(kind *acpsdk.ToolKind) acpsdk.RequestPermissionRequest {
		return acpsdk.RequestPermissionRequest{
			ToolCall: acpsdk.ToolCallUpdate{Kind: kind},
			Options:  []acpsdk.PermissionOption{{OptionId: "allow", Kind: acpsdk.PermissionOptionKindAllowOnce}},
		}
	}
	if _, ok := permissionPolicy(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions, req(&execKind)); !ok {
		t.Fatal("no policy must keep auto-allowing bypass")
	}
	d := policytest.Cap(t, permissioncap.AcceptEdits)
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if _, ok := permissionPolicy(d, mode, req(&execKind)); ok {
			t.Errorf("accept_edits cap: %q auto-allowed an exec tool", mode)
		}
		if _, ok := permissionPolicy(d, mode, req(&edit)); !ok {
			t.Errorf("accept_edits cap: %q must still allow edits", mode)
		}
	}
	// The auto cap only removes bypass; DeepSeek auto already allows everything.
	if _, ok := permissionPolicy(policytest.Cap(t, permissioncap.Auto), ports.PermissionModeAuto, req(&execKind)); !ok {
		t.Error("auto cap must keep auto behaviour")
	}
}

func TestLaunchFor_KeepsOnlyKnownSandboxedModeUnderCap(t *testing.T) {
	launch := func(d agentpolicy.Decision, env map[string]string) map[string]string {
		l, err := launchFor(stubPlugin{}, "/bin/dsh", env, t.TempDir(), ports.PermissionModeAuto, d)
		if err != nil {
			t.Fatal(err)
		}
		return l.Env
	}
	if got := launch(agentpolicy.Decision{}, map[string]string{"DSH_PERMISSION_MODE": "danger-full-access"}); got["DSH_PERMISSION_MODE"] != "danger-full-access" {
		t.Fatalf("no policy must pass the env through unchanged, got %v", got)
	}
	d := policytest.Cap(t, permissioncap.AcceptEdits)
	for _, value := range []string{"danger-full-access", " Danger-Full-Access ", "full-access", "yolo", "", "Workspace-Write"} {
		got := launch(d, map[string]string{"DSH_PERMISSION_MODE": value, "dsh_permission_mode": value, "OTHER": "kept"})
		for k, v := range got {
			if strings.EqualFold(k, "DSH_PERMISSION_MODE") {
				t.Errorf("value %q survived the cap: %s=%q", value, k, v)
			}
		}
		if got["OTHER"] != "kept" {
			t.Errorf("value %q: unrelated env dropped: %v", value, got)
		}
	}
	for _, value := range []string{"read-only", "workspace-write"} {
		if got := launch(d, map[string]string{"DSH_PERMISSION_MODE": value}); got["DSH_PERMISSION_MODE"] != value {
			t.Errorf("sandboxed value %q must be kept, got %q", value, got["DSH_PERMISSION_MODE"])
		}
	}
}
