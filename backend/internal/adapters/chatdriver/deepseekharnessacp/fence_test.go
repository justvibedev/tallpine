package deepseekharnessacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/deepseekharness"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/permissioncap"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

// pinFence points sandbox-exec at a fake (present) or a missing path. The
// adapter's own fenceGOOS is the host OS, so wrapping cases need darwin.
func pinFence(t *testing.T, present bool) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the seatbelt fence is macOS only")
	}
	oldGOOS, oldExec := fenceGOOS, statefence.SandboxExec
	t.Cleanup(func() { fenceGOOS, statefence.SandboxExec = oldGOOS, oldExec })
	fenceGOOS = "darwin"
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

func launchVia(t *testing.T, root string, d agentpolicy.Decision, mode ports.PermissionMode, env map[string]string) (acpdriver.Launch, error) {
	base := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "HOME": filepath.Join(root, "home")}
	for k, v := range env {
		base[k] = v
	}
	return buildConfig(stubPlugin{}, nil).Launch(context.Background(), acpdriver.LaunchConfig{
		Policy: d, Permissions: mode, Env: base, DataDir: root, WorkspacePath: root,
	})
}

// Bypass ACP launches run in the seatbelt; confined modes and a preset
// sandboxed DSH_PERMISSION_MODE do not. dsh's args and env are identical with
// and without the fence.
func TestACPLaunchWrappedOnlyWithoutDSHSandbox(t *testing.T) {
	for _, tc := range []struct {
		name    string
		d       agentpolicy.Decision
		mode    ports.PermissionMode
		env     map[string]string
		wrapped bool
	}{
		{"bypass", fenceOn(t), ports.PermissionModeBypassPermissions, nil, true},
		{"auto", fenceOn(t), ports.PermissionModeAuto, nil, false},
		{"accept-edits", fenceOn(t), ports.PermissionModeAcceptEdits, nil, false},
		{"default", fenceOn(t), ports.PermissionModeDefault, nil, false},
		{"bypass capped", policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}, permissioncap.Name: {permissioncap.FieldMax: string(permissioncap.AcceptEdits)}}), ports.PermissionModeBypassPermissions, nil, false},
		{"off", policytest.Decision(t, nil), ports.PermissionModeBypassPermissions, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinFence(t, true)
			root := t.TempDir()
			got, err := launchVia(t, root, tc.d, tc.mode, tc.env)
			if err != nil {
				t.Fatal(err)
			}
			exec := statefence.SandboxExec
			statefence.SandboxExec = filepath.Join(t.TempDir(), "missing")
			plain, err := launchVia(t, root, tc.d, tc.mode, tc.env)
			statefence.SandboxExec = exec
			if err != nil {
				t.Fatal(err)
			}
			wrapped := got.Command == statefence.SandboxExec && len(got.Args) > 2 && got.Args[0] == "-p" && got.Args[2] == "/bin/dsh"
			if wrapped != tc.wrapped {
				t.Fatalf("wrapped = %v, want %v: %q %q", wrapped, tc.wrapped, got.Command, got.Args)
			}
			argv := append([]string{got.Command}, got.Args...)
			if wrapped {
				argv = got.Args[2:]
			}
			if !slices.Equal(argv, append([]string{plain.Command}, plain.Args...)) || got.Env["DSH_PERMISSION_MODE"] != plain.Env["DSH_PERMISSION_MODE"] {
				t.Fatalf("dsh launch changed by the fence:\n%q %v\n%q %v", argv, got.Env, plain.Args, plain.Env)
			}
		})
	}
}

// Under the hardened profile a confined Chat launch cannot be fenced and is
// refused at the launch itself; a bypass one is fenced and admitted.
func TestHardenedRefusesConfinedACPLaunch(t *testing.T) {
	pinFence(t, true)
	var fenceErr *agentpolicy.FenceRequiredError
	if _, err := launchVia(t, t.TempDir(), policytest.Hardened(t), ports.PermissionModeAuto, nil); !errors.As(err, &fenceErr) {
		t.Fatalf("hardened auto launch err = %v, want FenceRequiredError", err)
	}
	if _, err := launchVia(t, t.TempDir(), policytest.Hardened(t), ports.PermissionModeBypassPermissions, nil); err != nil && !errors.As(err, &fenceErr) {
		t.Fatalf("hardened bypass launch: %v", err)
	}
}

// Drift: Chat wraps exactly when the terminal's FencesLaunch (what admission,
// the gap and the git config read) answers true, for every mode and env
// preset; a preset that contradicts the mode is refused, never misreported.
func TestACPFenceRuleMatchesTerminal(t *testing.T) {
	pinFence(t, true)
	driver := New(stubPlugin{}, nil)
	for _, mode := range []ports.PermissionMode{"", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
		if got, want := ports.FencesLaunch(driver, "darwin", mode), deepseekharness.FencesLaunch("darwin", mode); got != want {
			t.Errorf("mode %q: driver FencesLaunch %v, terminal %v", mode, got, want)
		}
		reported := deepseekharness.FencesLaunch("darwin", mode)
		for _, preset := range []string{"", "read-only", "workspace-write", "danger-full-access"} {
			env := map[string]string{}
			if preset != "" {
				env["DSH_PERMISSION_MODE"] = preset
			}
			got, err := launchVia(t, t.TempDir(), fenceOn(t), mode, env)
			// A preset is kept by launchFor; bypass sets full access only when unset.
			sandboxOff := preset == "danger-full-access" || preset == "" && mode == ports.PermissionModeBypassPermissions
			if sandboxOff != reported {
				if !errors.Is(err, deepseekharness.ErrFenceMismatch) {
					t.Errorf("mode %q env %q: want refusal, got %v", mode, preset, err)
				}
				continue
			}
			if err != nil {
				t.Fatalf("mode %q env %q: %v", mode, preset, err)
			}
			if wrapped := got.Command == statefence.SandboxExec; wrapped != reported {
				t.Errorf("mode %q env %q: wrapped %v, reported %v", mode, preset, wrapped, reported)
			}
		}
	}
}
