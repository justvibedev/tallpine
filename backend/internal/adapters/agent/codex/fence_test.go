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
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// pinFence makes the fence available (a fake sandbox-exec) on goos.
func pinFence(t *testing.T, goos string, present bool) {
	t.Helper()
	oldGOOS, oldExec := fenceGOOS, statefence.SandboxExec
	t.Cleanup(func() { fenceGOOS, statefence.SandboxExec = oldGOOS, oldExec })
	fenceGOOS = goos
	statefence.SandboxExec = filepath.Join(t.TempDir(), "sandbox-exec")
	if present {
		if err := os.WriteFile(statefence.SandboxExec, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func fenceOn(t *testing.T) agentpolicy.Decision {
	return policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
}

func TestFencesLaunchOnlyUnsandboxedOnDarwin(t *testing.T) {
	pinFence(t, "darwin", true)
	for _, mode := range []ports.PermissionMode{"", "bogus", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		want := mode != ports.PermissionModeAcceptEdits && mode != ports.PermissionModeAuto
		if got := New().FencesLaunch("darwin", mode); got != want {
			t.Errorf("darwin %q = %v, want %v", mode, got, want)
		}
		if New().FencesLaunch("linux", mode) {
			t.Errorf("linux %q fenced", mode)
		}
	}
	if !ports.FencesLaunch(New(), "darwin", ports.PermissionModeBypassPermissions) || ports.FencesState(New(), "darwin") {
		t.Fatal("ports must reach FencesLaunch; Codex has no mode-blind FencesState")
	}
}

// Launches and restores with no Codex sandbox are wrapped; sandboxed modes are not, and
// Codex's own flags are identical with and without the fence.
func TestLaunchAndRestoreWrappedOnlyWithoutCodexSandbox(t *testing.T) {
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	env := map[string]string{brand.Env("RUN_FILE"): runFile, "HOME": filepath.Join(root, "home"), "CODEX_HOME": filepath.Join(root, "acct")}
	plugin := &Plugin{resolvedBinary: "codex"}
	launch := func(d agentpolicy.Decision, mode ports.PermissionMode) []string {
		cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Env: env, Policy: d, WorkspacePath: root, Permissions: mode})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	restore := func(d agentpolicy.Decision, mode ports.PermissionMode) []string {
		cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			Policy: d, RunFilePath: runFile, Env: env, Permissions: mode,
			Session: ports.SessionRef{WorkspacePath: root, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "019a0000-0000-7000-8000-000000000001"}},
		})
		if err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		return cmd
	}
	for _, tc := range []struct {
		name    string
		goos    string
		d       agentpolicy.Decision
		mode    ports.PermissionMode
		wrapped bool
	}{
		{"bypass darwin", "darwin", fenceOn(t), ports.PermissionModeBypassPermissions, true},
		{"bypass linux", "linux", fenceOn(t), ports.PermissionModeBypassPermissions, false},
		{"auto", "darwin", fenceOn(t), ports.PermissionModeAuto, false},
		{"accept-edits", "darwin", fenceOn(t), ports.PermissionModeAcceptEdits, false},
		{"default", "darwin", fenceOn(t), ports.PermissionModeDefault, true},
		{"empty", "darwin", fenceOn(t), "", true},
		{"off", "darwin", policytest.Decision(t, nil), ports.PermissionModeBypassPermissions, false},
		{"hardened auto", "darwin", policytest.Hardened(t), ports.PermissionModeAuto, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinFence(t, tc.goos, true)
			for name, build := range map[string]func(agentpolicy.Decision, ports.PermissionMode) []string{"launch": launch, "restore": restore} {
				cmd := build(tc.d, tc.mode)
				// The same launch with the fence unavailable: only the fence differs.
				exec := statefence.SandboxExec
				statefence.SandboxExec = filepath.Join(root, "missing")
				plain := build(tc.d, tc.mode)
				statefence.SandboxExec = exec
				wrapped := cmd[0] == statefence.SandboxExec && cmd[1] == "-p" && cmd[3] == "codex"
				if wrapped != tc.wrapped {
					t.Fatalf("%s wrapped = %v, want %v: %q", name, wrapped, tc.wrapped, cmd)
				}
				inner := cmd
				if wrapped {
					inner = cmd[3:]
					if !strings.Contains(cmd[2], "/home/.codex\")") || !strings.Contains(cmd[2], "/acct\")") {
						t.Fatalf("%s profile lacks codex homes: %s", name, cmd[2])
					}
				}
				if !slices.Equal(inner, plain) {
					t.Fatalf("%s codex flags changed by the fence:\n%q\n%q", name, inner, plain)
				}
			}
		})
	}
}

func TestHardenedBypassReAllowsCodexAuth(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "HOME": filepath.Join(root, "home")}
	pinFence(t, "darwin", true)
	cmd, err := Fence([]string{"codex"}, env, policytest.Hardened(t), ports.PermissionModeBypassPermissions, root)
	if err != nil {
		t.Fatal(err)
	}
	allow := cmd[2][strings.LastIndex(cmd[2], "(allow file-read*"):]
	if !strings.Contains(allow, "/home/.aws\")") || !strings.Contains(allow, "/home/.config\")") {
		t.Fatalf("hardened profile does not re-allow codex auth: %s", cmd[2])
	}
}
