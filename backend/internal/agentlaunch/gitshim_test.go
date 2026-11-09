package agentlaunch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakeTool(t *testing.T, dir, name, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // test fake must be executable
		t.Fatal(err)
	}
	return path
}

// The real sh shim: only remote/config reach tallpine, everything else is the
// real git with its arguments intact, and the shim never finds itself.
func TestGitShimRoutesOnlyRemoteAndConfigToTallpine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim")
	}
	root := t.TempDir()
	exe := writeFakeTool(t, filepath.Join(root, "bin dir"), "tallpine", `printf 'GUARD:'; for a in "$@"; do printf '[%s]' "$a"; done; echo`)
	realDir := filepath.Join(root, "real")
	writeFakeTool(t, realDir, "git", `printf 'REAL:'; for a in "$@"; do printf '[%s]' "$a"; done; echo`)
	dir, err := EnsureGitShim(exe, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	run := func(path string, args ...string) (string, int) {
		cmd := exec.Command(filepath.Join(dir, "git"), args...)
		cmd.Env = []string{"PATH=" + path}
		out, err := cmd.CombinedOutput()
		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out)), code
	}
	path := dir + ":" + realDir + ":/usr/bin:/bin"
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"status", "--short"}, "REAL:[status][--short]"},
		{[]string{"commit", "-m", "two words"}, "REAL:[commit][-m][two words]"},
		{[]string{"push", "-u", "origin", "HEAD"}, "REAL:[push][-u][origin][HEAD]"},
		{[]string{"remote", "add", "x", "u"}, "GUARD:[git-exec][" + dir + "][--][remote][add][x][u]"},
		{[]string{"-C", "/a b", "config", "k.v", "1"}, "GUARD:[git-exec][" + dir + "][--][-C][/a b][config][k.v][1]"},
	} {
		if got, code := run(path, tc.args...); got != tc.want || code != 0 {
			t.Errorf("%v: got %q (exit %d), want %q", tc.args, got, code, tc.want)
		}
	}
	if got, code := run(dir+":/nonexistent", "status"); code != 127 || !strings.Contains(got, "git not found") {
		t.Fatalf("no real git: %q exit %d", got, code)
	}
}

// A daemon started from inside a session must not hand its guard settings, or
// a one-command override, to the sessions it launches.
func TestInheritedEnvDropsTheGitGuardVariables(t *testing.T) {
	got := InheritedHostIdentityKeys([]string{EnvAllowSharedGitConfig + "=1", EnvGitGuardRoot + "=/x", "HOME=/h"})
	if len(got) != 2 {
		t.Fatalf("scrubbed %v, want both guard variables and nothing else", got)
	}
}

// The launch binary's directory can hold the real git; adding it must not put
// that git ahead of the shim. The pinned tallpine directory stays first.
func TestAugmentKeepsTheGitShimAheadOfTheLaunchBinaryDir(t *testing.T) {
	shim := filepath.Join("/data", "runtime", gitShimParent, "abc")
	for name, tc := range map[string]struct{ pinned, path, want string }{
		"pinned":     {"/pin", "/pin:/gh:" + shim + ":/usr/bin", "/pin:" + shim + ":/agent:/gh:/usr/bin"},
		"no pin":     {"", "/gh:" + shim + ":/usr/bin", shim + ":/agent:/gh:/usr/bin"},
		"no shim":    {"", "/usr/bin", "/agent:/usr/bin"},
		"already on": {"", shim + ":/agent:/usr/bin", shim + ":/agent:/usr/bin"},
	} {
		env := map[string]string{"PATH": tc.path}
		AugmentRuntimePATHForLaunchBinary(context.Background(), env, []string{"/agent/claude"}, func(string) (string, error) { return "", errors.New("none") }, tc.pinned)
		if env["PATH"] != tc.want {
			t.Errorf("%s: PATH = %q, want %q", name, env["PATH"], tc.want)
		}
	}
}
