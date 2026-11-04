package codex

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func hasSandboxWorkspaceWrite(argv []string) bool {
	for i, a := range argv {
		if a == "--sandbox" && i+1 < len(argv) && argv[i+1] == "workspace-write" {
			return true
		}
	}
	return false
}

func TestTerminalLaunchUnderCapPinsWorkspaceWriteSandbox(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	launch := func(d agentpolicy.Decision, mode ports.PermissionMode) []string {
		cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: d, Permissions: mode, Prompt: "x", WorkspacePath: canonicalTempDir(t)})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	// No policy: argv is unchanged (bypass stays a bypass, no --sandbox).
	cmd := launch(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions)
	if hasSandboxWorkspaceWrite(cmd) || !slices.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("default behaviour changed: %v", cmd)
	}
	for _, level := range []permissioncap.Level{permissioncap.Auto, permissioncap.AcceptEdits} {
		d := policytest.Cap(t, level)
		for _, mode := range []ports.PermissionMode{ports.PermissionModeBypassPermissions, ports.PermissionModeDefault, ports.PermissionModeAuto, ports.PermissionModeAcceptEdits} {
			cmd := launch(d, mode)
			if !hasSandboxWorkspaceWrite(cmd) || slices.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
				t.Errorf("cap %s mode %q: argv lacks the pinned sandbox or still bypasses: %v", level, mode, cmd)
			}
		}
	}
}

func TestTerminalRestoreUnderCapPinsWorkspaceWriteSandbox(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
		Policy:      policytest.Cap(t, permissioncap.Auto),
		Permissions: ports.PermissionModeBypassPermissions,
		Session: ports.SessionRef{
			ID:            "session-123",
			Metadata:      map[string]string{ports.MetadataKeyAgentSessionID: "thread-123"},
			WorkspacePath: canonicalTempDir(t),
		},
	})
	if err != nil || !ok {
		t.Fatalf("restore: ok=%v err=%v", ok, err)
	}
	if !hasSandboxWorkspaceWrite(cmd) || slices.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("restored argv not sandboxed: %v", cmd)
	}
}

func TestTerminalWorkspaceWriteLaunchEnablesNetworkAndCaches(t *testing.T) {
	plugin := &Plugin{resolvedBinary: "codex"}
	launch := func(d agentpolicy.Decision, mode ports.PermissionMode) []string {
		cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: d, Permissions: mode, Prompt: "x", WorkspacePath: canonicalTempDir(t)})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	check := func(name string, args []string) {
		t.Helper()
		if !slices.Contains(args, "sandbox_workspace_write.network_access=true") {
			t.Errorf("%s lacks network_access=true: %v", name, args)
		}
		hasRoots := false
		for _, a := range args {
			if strings.HasPrefix(a, "sandbox_workspace_write.writable_roots=") && strings.Contains(a, ".gradle/caches") && strings.Contains(a, "verify-slots") {
				hasRoots = true
			}
		}
		if !hasRoots {
			t.Errorf("%s lacks writable roots: %v", name, args)
		}
	}
	check("capped bypass request", launch(policytest.Cap(t, permissioncap.Auto), ports.PermissionModeBypassPermissions))
	for _, mode := range []ports.PermissionMode{ports.PermissionModeAcceptEdits, ports.PermissionModeAuto} {
		check("uncapped "+string(mode), launch(agentpolicy.Decision{}, mode))
	}
	// Bypass has no sandbox: nothing to configure.
	for _, a := range launch(agentpolicy.Decision{}, ports.PermissionModeBypassPermissions) {
		if strings.Contains(a, "sandbox_workspace_write") {
			t.Errorf("uncapped bypass launch must be unchanged: %s", a)
		}
	}
}

func TestCappedLaunchMakesLinkedWorktreeGitDirWritable(t *testing.T) {
	ws, gitdir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var args []string
	capCodexPermission(policytest.Cap(t, permissioncap.Auto), ports.PermissionModeAuto, ws, &args)
	if !strings.Contains(strings.Join(args, " "), gitdir) {
		t.Errorf("git fetch needs the worktree git dir writable: %v", args)
	}
}

// workspaceWriteExtrasArgs is what a workspace-write launch appends for the
// network and writable roots, for tests that pin a whole argv.
func workspaceWriteExtrasArgs(workspace string) []string {
	var args []string
	appendWorkspaceWriteExtras(&args, workspace)
	return args
}
