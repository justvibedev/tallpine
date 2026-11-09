package codexappserver

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The conversation's own decision, frozen at start, decides what its sandbox
// allows: a turn asking for bypass on a capped chat gets no network.
func TestSandboxAllowsNetworkUsesTheConversationsDecision(t *testing.T) {
	free := &conversation{launchMode: ports.PermissionModeBypassPermissions}
	if !free.SandboxAllowsNetwork("") {
		t.Fatal("a bypass chat with no policy has full access")
	}
	capped := policytest.Cap(t, permissioncap.Auto)
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeBypassPermissions} {
		if _, sandbox := approvalSettings(capped, mode); sandbox != "workspace-write" {
			t.Fatalf("a capped chat must not get the full-access sandbox, whatever the turn asks (%q): %s", mode, sandbox)
		}
	}
}

// Any chat in workspace-write (capped or not) gets network and the build
// caches on both the thread and every turn; bypass stays full access.
func TestWorkspaceWriteChatSandboxCarriesNetworkAndWritableRoots(t *testing.T) {
	capped := policytest.Cap(t, permissioncap.Auto)
	for name, decision := range map[string]agentpolicy.Decision{"capped": capped, "uncapped": {}} {
		turn := turnSandboxPolicy("workspace-write", "", "")
		if turn["type"] != "workspaceWrite" || turn["networkAccess"] != true || turn["writableRoots"] == nil {
			t.Errorf("%s turn policy = %v", name, turn)
		}
		for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto} {
			cfg, _ := threadStartParams(threadSettings{Policy: decision, Permissions: mode})["config"].(map[string]any)
			ws, _ := cfg["sandbox_workspace_write"].(map[string]any)
			if ws["network_access"] != true || ws["writable_roots"] == nil {
				t.Errorf("%s/%s thread config = %v", name, mode, cfg)
			}
			if !(&conversation{launchMode: mode, policy: decision}).SandboxAllowsNetwork("") {
				t.Errorf("%s/%s workspace-write must report network", name, mode)
			}
		}
	}
	if _, ok := threadStartParams(threadSettings{Permissions: ports.PermissionModeBypassPermissions})["config"]; ok {
		t.Error("bypass thread start must carry no sandbox config")
	}
}
