package deepseekharness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// pinFence makes the fence available (a fake sandbox-exec) on goos, or not.
func pinFence(t *testing.T, goos string, present bool) {
	t.Helper()
	oldGOOS, oldExec := fenceGOOS, statefence.SandboxExec
	t.Cleanup(func() { fenceGOOS, statefence.SandboxExec = oldGOOS, oldExec })
	fenceGOOS = goos
	statefence.SandboxExec = filepath.Join(t.TempDir(), "sandbox-exec")
	if present {
		if err := os.WriteFile(statefence.SandboxExec, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test stub
			t.Fatal(err)
		}
	}
}

func fenceOn(t *testing.T) agentpolicy.Decision {
	return policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
}

func TestFencesLaunchOnlyBypassOnDarwin(t *testing.T) {
	pinFence(t, "darwin", true)
	for _, mode := range []ports.PermissionMode{"", "bogus", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		want := mode == ports.PermissionModeBypassPermissions
		if got := ports.FencesLaunch(New(), "darwin", mode); got != want {
			t.Errorf("darwin %q = %v, want %v", mode, got, want)
		}
		if New().FencesLaunch("linux", mode) {
			t.Errorf("linux %q fenced", mode)
		}
	}
	if ports.FencesState(New(), "darwin") {
		t.Fatal("dsh has no mode-blind FencesState")
	}
}

func TestTerminalSandboxOffReadsBypassAndEnv(t *testing.T) {
	for _, tc := range []struct {
		mode ports.PermissionMode
		env  string
		want bool
	}{
		{ports.PermissionModeBypassPermissions, "", true},
		{ports.PermissionModeBypassPermissions, "workspace-write", true}, // the full-access patch outranks the env
		{ports.PermissionModeAuto, "", false},
		{ports.PermissionModeDefault, "", false},
		{ports.PermissionModeAuto, "danger-full-access", true},
		{ports.PermissionModeAuto, "workspace-write", false},
		{ports.PermissionModeAuto, " danger-full-access", false}, // dsh rejects it; not unsandboxed
	} {
		if got := terminalSandboxOff(tc.mode, map[string]string{PermissionModeEnv: tc.env}); got != tc.want {
			t.Errorf("terminalSandboxOff(%q, %q) = %v, want %v", tc.mode, tc.env, got, tc.want)
		}
	}
}

// Under Security mode on macOS, a launch is wrapped exactly when FencesLaunch
// (what admission, the gap and the git config read) says so; an env preset
// that contradicts it is refused, on launch and restore alike.
func TestTerminalFenceMatchesReportedAnswer(t *testing.T) {
	fakeDSH(t)
	pinFence(t, "darwin", true)
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		for _, preset := range []string{"", "read-only", "workspace-write", FullAccessMode} {
			env := map[string]string{brand.Env("RUN_FILE"): runFile, "HOME": filepath.Join(root, "home")}
			if preset != "" {
				env[PermissionModeEnv] = preset
			}
			cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: root, Env: env, Policy: fenceOn(t), WorkspacePath: root, Permissions: mode})
			_, _, rerr := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{Prompt: "x", Policy: fenceOn(t), RunFilePath: runFile, DataDir: root, Env: env, Permissions: mode,
				Session: ports.SessionRef{WorkspacePath: root, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "s-1"}}})
			mismatch := FencesLaunch("darwin", mode) != terminalSandboxOff(mode, env)
			if mismatch {
				if !errors.Is(err, ErrFenceMismatch) || !errors.Is(rerr, ErrFenceMismatch) {
					t.Errorf("%q env %q: want refusal, got %v / %v", mode, preset, err, rerr)
				}
				continue
			}
			if err != nil || rerr != nil {
				t.Fatalf("%q env %q: %v / %v", mode, preset, err, rerr)
			}
			if wrapped := cmd[0] == statefence.SandboxExec; wrapped != FencesLaunch("darwin", mode) {
				t.Errorf("%q env %q: wrapped %v, reported %v", mode, preset, wrapped, FencesLaunch("darwin", mode))
			}
		}
	}
}

// Bypass launches and restores are wrapped; confined modes are not, and dsh's
// patches and flags are identical with and without the fence.
func TestLaunchAndRestoreWrappedOnlyWithoutDSHSandbox(t *testing.T) {
	bin := fakeDSH(t)
	root := t.TempDir()
	runFile := filepath.Join(root, "running.json")
	dataDir := filepath.Join(root, "data")
	baseEnv := map[string]string{brand.Env("RUN_FILE"): runFile, "HOME": filepath.Join(root, "home"), "DSH_HOME": filepath.Join(root, "dshhome")}
	for _, tc := range []struct {
		name    string
		goos    string
		d       agentpolicy.Decision
		mode    ports.PermissionMode
		env     string
		wrapped bool
	}{
		{"bypass darwin", "darwin", fenceOn(t), ports.PermissionModeBypassPermissions, "", true},
		{"bypass linux", "linux", fenceOn(t), ports.PermissionModeBypassPermissions, "", false},
		{"auto", "darwin", fenceOn(t), ports.PermissionModeAuto, "", false},
		{"accept-edits", "darwin", fenceOn(t), ports.PermissionModeAcceptEdits, "", false},
		{"default", "darwin", fenceOn(t), ports.PermissionModeDefault, "", false},
		{"empty", "darwin", fenceOn(t), "", "", false},
		{"auto with env full access, fence off", "darwin", policytest.Decision(t, nil), ports.PermissionModeAuto, FullAccessMode, false},
		{"bypass capped to accept-edits", "darwin", policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}, permissioncap.Name: {permissioncap.FieldMax: string(permissioncap.AcceptEdits)}}), ports.PermissionModeBypassPermissions, "", false},
		{"off", "darwin", policytest.Decision(t, nil), ports.PermissionModeBypassPermissions, "", false},
		{"hardened auto", "darwin", policytest.Hardened(t), ports.PermissionModeAuto, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinFence(t, tc.goos, true)
			env := map[string]string{}
			for k, v := range baseEnv {
				env[k] = v
			}
			if tc.env != "" {
				env[PermissionModeEnv] = tc.env
			}
			launch := func() []string {
				cmd, err := New().GetLaunchCommand(context.Background(), ports.LaunchConfig{Prompt: "x", DataDir: dataDir, Env: env, Policy: tc.d, WorkspacePath: root, Permissions: tc.mode})
				if err != nil {
					t.Fatal(err)
				}
				return cmd
			}
			restore := func() []string {
				cmd, ok, err := New().GetRestoreCommand(context.Background(), ports.RestoreConfig{
					Prompt: "x", Policy: tc.d, RunFilePath: runFile, DataDir: dataDir, Env: env, Permissions: tc.mode,
					Session: ports.SessionRef{WorkspacePath: root, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "s-1"}},
				})
				if err != nil || !ok {
					t.Fatalf("restore: ok=%v err=%v", ok, err)
				}
				return cmd
			}
			for name, build := range map[string]func() []string{"launch": launch, "restore": restore} {
				cmd := build()
				exec := statefence.SandboxExec
				statefence.SandboxExec = filepath.Join(root, "missing")
				plain := build()
				statefence.SandboxExec = exec
				wrapped := cmd[0] == statefence.SandboxExec && cmd[1] == "-p" && cmd[3] == bin
				if wrapped != tc.wrapped {
					t.Fatalf("%s wrapped = %v, want %v: %q", name, wrapped, tc.wrapped, cmd)
				}
				inner := cmd
				if wrapped {
					inner = cmd[3:]
					if !strings.Contains(cmd[2], "/home/.dsh\")") || !strings.Contains(cmd[2], "/dshhome\")") {
						t.Fatalf("%s profile lacks dsh homes: %s", name, cmd[2])
					}
				}
				if !slices.Equal(inner, plain) {
					t.Fatalf("%s dsh flags changed by the fence:\n%q\n%q", name, inner, plain)
				}
			}
		})
	}
}

func TestHardenedBypassReAllowsDSHAuth(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "HOME": filepath.Join(root, "home")}
	pinFence(t, "darwin", true)
	cmd, err := Fence([]string{"dsh"}, env, policytest.Hardened(t), ports.PermissionModeBypassPermissions, true, root)
	if err != nil {
		t.Fatal(err)
	}
	allow := cmd[2][strings.LastIndex(cmd[2], "(allow file-read*"):]
	if !strings.Contains(allow, "/home/.dsh\")") {
		t.Fatalf("hardened profile does not re-allow dsh auth: %s", cmd[2])
	}
}
