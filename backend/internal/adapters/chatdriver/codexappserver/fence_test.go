package codexappserver

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/persistenthost"
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules/protectstate"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/statefence"
)

var errStopHost = errors.New("stop after capture")

// pinSandboxExec points the fence at a fake sandbox-exec. The Codex agent
// package checks the real host OS, so wrapping tests run on macOS only.
func pinSandboxExec(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the Codex fence wraps on macOS only")
	}
	old := statefence.SandboxExec
	t.Cleanup(func() { statefence.SandboxExec = old })
	statefence.SandboxExec = filepath.Join(t.TempDir(), "sandbox-exec")
	if err := os.WriteFile(statefence.SandboxExec, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestDriverFencesLaunchOnlyWithoutCodexSandbox(t *testing.T) {
	pinSandboxExec(t)
	d := New(fakePlugin{bin: "codex"}, slog.Default())
	for mode, want := range map[ports.PermissionMode]bool{
		"": true, ports.PermissionModeDefault: true, ports.PermissionModeBypassPermissions: true,
		ports.PermissionModeAuto: false, ports.PermissionModeAcceptEdits: false,
	} {
		if got := ports.FencesLaunch(d, "darwin", mode); got != want {
			t.Errorf("%q = %v, want %v", mode, got, want)
		}
		if d.FencesLaunch("linux", mode) {
			t.Errorf("linux %q fenced", mode)
		}
	}
}

// captureArgv runs a Start or Resume against a host stub and returns the
// app-server argv the host would launch (after Prepare when there is one).
func captureArgv(t *testing.T, run func(*Driver) error) ([]string, error) {
	t.Helper()
	d := New(fakePlugin{bin: "codex"}, slog.Default())
	var argv []string
	var prepErr error
	d.connectHost = func(ctx context.Context, cfg persistenthost.Config) (*persistenthost.Transport, error) {
		argv = cfg.Argv
		if cfg.Prepare != nil {
			var p persistenthost.PreparedProvider
			p, prepErr = cfg.Prepare(ctx)
			argv = p.Argv
		}
		return nil, errStopHost
	}
	err := run(d)
	if prepErr != nil {
		return nil, prepErr
	}
	if !errors.Is(err, errStopHost) {
		return nil, err
	}
	return argv, nil
}

func TestAppServerArgvWrappedOnlyWithoutCodexSandbox(t *testing.T) {
	pinSandboxExec(t)
	ws := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(ws, "running.json"), "HOME": filepath.Join(ws, "home")}
	on := policytest.Decision(t, map[string]map[string]any{protectstate.Name: {protectstate.FieldMode: protectstate.On}})
	start := func(d agentpolicy.Decision, mode ports.PermissionMode, readOnly, prepare bool) func(*Driver) error {
		return func(dr *Driver) error {
			cfg := ports.ChatStartConfig{SessionID: "s1", DataDir: ws, WorkspacePath: ws, Policy: d, Permissions: mode, ReadOnly: readOnly, Env: env}
			if prepare {
				cfg.Env = nil
				cfg.PrepareEnv = func(context.Context) (map[string]string, error) { return env, nil }
			}
			_, err := dr.Start(context.Background(), cfg)
			return err
		}
	}
	resume := func(d agentpolicy.Decision, mode ports.PermissionMode) func(*Driver) error {
		return func(dr *Driver) error {
			_, err := dr.Resume(context.Background(), ports.ChatResumeConfig{SessionID: "s1", ProviderConversationID: "t1", DataDir: ws, WorkspacePath: ws, Policy: d, Permissions: mode, Env: env})
			return err
		}
	}
	for _, tc := range []struct {
		name    string
		run     func(*Driver) error
		wrapped bool
	}{
		{"start bypass", start(on, ports.PermissionModeBypassPermissions, false, false), true},
		{"start bypass prepared", start(on, ports.PermissionModeBypassPermissions, false, true), true},
		{"start default", start(on, ports.PermissionModeDefault, false, false), true},
		{"resume bypass", resume(on, ports.PermissionModeBypassPermissions), true},
		{"start auto", start(on, ports.PermissionModeAuto, false, false), false},
		{"start accept-edits", start(on, ports.PermissionModeAcceptEdits, false, false), false},
		{"read-only bypass", start(on, ports.PermissionModeBypassPermissions, true, false), false},
		{"resume auto", resume(on, ports.PermissionModeAuto), false},
		{"off bypass", start(policytest.Decision(t, nil), ports.PermissionModeBypassPermissions, false, false), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv, err := captureArgv(t, tc.run)
			if err != nil {
				t.Fatal(err)
			}
			wrapped := len(argv) == 5 && argv[0] == statefence.SandboxExec && argv[1] == "-p"
			if wrapped != tc.wrapped {
				t.Fatalf("wrapped = %v, want %v: %q", wrapped, tc.wrapped, argv)
			}
			if !slices.Equal(argv[len(argv)-2:], []string{"codex", "app-server"}) {
				t.Fatalf("app-server argv changed: %q", argv)
			}
		})
	}
}

// The driver refuses a hardened launch whose Codex sandbox it cannot nest,
// even if a caller skipped admission.
func TestAppServerRefusesHardenedSandboxedLaunch(t *testing.T) {
	pinSandboxExec(t)
	old := fenceGOOS
	t.Cleanup(func() { fenceGOOS = old })
	fenceGOOS = "darwin"
	ws := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(ws, "running.json")}
	_, err := captureArgv(t, func(d *Driver) error {
		_, err := d.Start(context.Background(), ports.ChatStartConfig{SessionID: "s1", DataDir: ws, WorkspacePath: ws, Policy: policytest.Hardened(t), Permissions: ports.PermissionModeAuto, Env: env})
		return err
	})
	if !agentpolicy.IsFenceRequired(err) {
		t.Fatalf("err = %v, want fence required", err)
	}
}

// The fence rule and the thread's sandbox param agree for every mode and cap,
// so the driver never fences a thread that runs a Codex sandbox, or skips one
// that runs none.
func TestLaunchSandboxedMatchesThreadSandbox(t *testing.T) {
	decisions := map[string]agentpolicy.Decision{
		"off":          policytest.Decision(t, nil),
		"auto":         policytest.Decision(t, map[string]map[string]any{"permissionCap": {"max": "auto"}}),
		"accept-edits": policytest.Decision(t, map[string]map[string]any{"permissionCap": {"max": "accept-edits"}}),
	}
	for name, d := range decisions {
		for _, mode := range []ports.PermissionMode{"", "bogus", ports.PermissionModeDefault, ports.PermissionModeAcceptEdits, ports.PermissionModeAuto, ports.PermissionModeBypassPermissions} {
			for _, readOnly := range []bool{false, true} {
				_, sandbox, _ := launchApprovalSettings(d, mode, readOnly)
				if got, want := launchSandboxed(d, mode, readOnly), sandbox != "danger-full-access"; got != want {
					t.Errorf("%s %q readOnly=%v: sandboxed = %v, thread sandbox %q", name, mode, readOnly, got, sandbox)
				}
			}
		}
	}
}
