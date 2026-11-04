package kiro

import (
	"context"
	"os"
	"os/exec"
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

func TestLaunchAndRestoreWrappedOnlyWhenFenceApplies(t *testing.T) {
	root := t.TempDir()
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(root, "running.json"), "HOME": filepath.Join(root, "home")}
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	launch := func(d agentpolicy.Decision) []string {
		cmd, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Env: env, Policy: d, WorkspacePath: root})
		if err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	restore := func(d agentpolicy.Decision) []string {
		cmd, ok, err := plugin.GetRestoreCommand(context.Background(), ports.RestoreConfig{
			Policy: d, RunFilePath: env[brand.Env("RUN_FILE")],
			Session: ports.SessionRef{WorkspacePath: root, Metadata: map[string]string{ports.MetadataKeyAgentSessionID: "u-1"}},
		})
		if err != nil || !ok {
			t.Fatalf("restore: ok=%v err=%v", ok, err)
		}
		return cmd
	}
	for _, tc := range []struct {
		name    string
		goos    string
		present bool
		d       agentpolicy.Decision
		wrapped bool
	}{
		{"on darwin", "darwin", true, fenceOn(t), true},
		{"hardened darwin", "darwin", true, policytest.Hardened(t), true},
		{"off darwin", "darwin", true, policytest.Decision(t, nil), false},
		{"on linux", "linux", true, fenceOn(t), false},
		{"no sandbox-exec", "darwin", false, fenceOn(t), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinFence(t, tc.goos, tc.present)
			for name, cmd := range map[string][]string{"launch": launch(tc.d), "restore": restore(tc.d)} {
				wrapped := cmd[0] == statefence.SandboxExec && cmd[1] == "-p" && cmd[3] == "kiro-cli"
				if wrapped != tc.wrapped {
					t.Fatalf("%s wrapped = %v, want %v: %q", name, wrapped, tc.wrapped, cmd)
				}
				if wrapped && (!strings.Contains(cmd[2], "/.kiro\")") || !slices.Contains(cmd, "--trust-workspace")) {
					t.Fatalf("%s profile lacks kiro state or argv changed: %q", name, cmd)
				}
			}
		})
	}
}

// A fenced launch whose env cannot locate Tallpine state fails, never starts
// behind an empty profile.
func TestLaunchFailsClosedWithoutRunFile(t *testing.T) {
	pinFence(t, "darwin", true)
	plugin := &Plugin{resolvedBinary: "kiro-cli"}
	if _, err := plugin.GetLaunchCommand(context.Background(), ports.LaunchConfig{Policy: fenceOn(t)}); err == nil {
		t.Fatal("fenced launch without a run file must fail")
	}
}

func TestFencesStateNeedsDarwinAndSandboxExec(t *testing.T) {
	pinFence(t, "darwin", true)
	p := New()
	if !ports.FencesState(p, "darwin") || ports.FencesState(p, "linux") {
		t.Fatal("kiro fences on darwin only")
	}
	pinFence(t, "darwin", false)
	if ports.FencesState(p, "darwin") {
		t.Fatal("kiro cannot fence without sandbox-exec")
	}
}

// The fence protects Tallpine state, not Kiro: under the hardened profile Kiro
// keeps read-write access to ~/.kiro, its Application Support dir and ~/.aws,
// while Tallpine state stays denied.
func TestHardenedKiroFenceKeepsKiroAccessAndDeniesState(t *testing.T) {
	if !statefence.Available("darwin") {
		t.Skip("host cannot fence")
	}
	home := t.TempDir()
	app := filepath.Join(home, "Library", "Application Support", "kiro-cli")
	state := filepath.Join(home, "state")
	for _, d := range []string{filepath.Join(home, ".kiro", "settings"), filepath.Join(home, ".aws", "sso", "cache"), app, filepath.Join(state, "data")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(state, "data", "ao.db")
	if err := os.WriteFile(db, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{brand.Env("RUN_FILE"): filepath.Join(state, "running.json"), "HOME": home}
	profile, err := statefence.Profile(env, policytest.Hardened(t), "", HarnessPaths)
	if err != nil {
		t.Fatal(err)
	}
	run := func(argv ...string) error {
		return exec.Command(statefence.SandboxExec, append([]string{"-p", profile}, argv...)...).Run()
	}
	for _, p := range []string{filepath.Join(home, ".kiro", "settings", "cli.json"), filepath.Join(app, "data.sqlite3"), filepath.Join(app, "bun"), filepath.Join(home, ".aws", "config"), filepath.Join(home, ".aws", "sso", "cache", "t.json")} {
		if err := run("/bin/sh", "-c", "echo x > \"$1\" && cat \"$1\"", "sh", p); err != nil {
			t.Errorf("fenced read-write of %s failed: %v", p, err)
		}
	}
	if run("cat", db) == nil {
		t.Error("Tallpine state must stay denied")
	}
}
