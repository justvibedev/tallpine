//go:build windows

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The probes carry double-quoted hook config. cmd.exe re-parses a .cmd shim's
// arguments and mangles those quotes, so the canary must run the native
// codex.exe the shim wraps, not the shim itself.
func TestDoctorCodexLaunchFlagsRunsNativeExeBehindNpmShim(t *testing.T) {
	setConfigEnv(t)
	dir := t.TempDir()
	shim := filepath.Join(dir, "codex.cmd")
	native := filepath.Join(dir, "node_modules", "@openai", "codex", "node_modules", "@openai", "codex-win32-x64", "vendor", "x86_64-pc-windows-msvc", "bin", "codex.exe")
	for _, f := range []string{shim, native} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var probed []string
	fake := func(_ context.Context, name string, args ...string) ([]byte, error) {
		if len(args) == 1 && args[0] == "--version" {
			return []byte("codex-cli 0.169.0\n"), nil
		}
		if filepath.Base(name) == "git.exe" {
			return []byte("git version 2.56.0\n"), nil
		}
		probed = append(probed, name)
		return []byte("ok\n"), nil
	}
	c := doctorContext(t, map[string]string{"git": "git.exe", "codex": shim}, fake)

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "codex-launch-flags")
	if check.Level != doctorPass {
		t.Fatalf("canary = %+v, want PASS", check)
	}
	for _, name := range probed {
		if name != native {
			t.Fatalf("probe ran %q, want native %q", name, native)
		}
	}
	if len(probed) == 0 {
		t.Fatal("no launch probe ran")
	}
}
