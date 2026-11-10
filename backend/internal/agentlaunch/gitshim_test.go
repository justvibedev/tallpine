package agentlaunch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
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

// A wrapper git on PATH that calls the shim's own path used to ping-pong with
// the shim forever. The shim now moves on to the next git, or stops with an
// error when there is none, and leaves no process behind. Covers exec and
// child-process wrappers and the remote/config route through `tallpine git-exec`.
func TestGitShimDoesNotLoopThroughAWrapperGit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh shim")
	}
	root := t.TempDir()
	// Stands in for `tallpine git-exec <dir> -- args`: runs the first git on
	// PATH outside the shim directory as a child, like findOnPathOutside.
	exe := writeFakeTool(t, filepath.Join(root, "bin"), "tallpine", `shim=$2; shift 3
IFS=:
for d in $PATH; do
  [ "$d" = "$shim" ] && continue
  [ -x "$d/git" ] && { "$d/git" "$@"; exit $?; }
done
exit 127`)
	dir, err := EnsureGitShim(exe, filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	pids := filepath.Join(root, "pids")
	shimGit := filepath.Join(dir, "git")
	realDir := filepath.Join(root, "real")
	writeFakeTool(t, realDir, "git", `echo "REAL $*"`)
	for name, body := range map[string]string{
		"exec":  `printf 'WRAP '; echo $$ >> '` + pids + `'; exec '` + shimGit + `' "$@"`,
		"twice": `echo $$ >> '` + pids + `'; '` + shimGit + `' rev-parse HEAD >/dev/null; '` + shimGit + `' "$@"`,
		"child": `printf 'WRAP '; echo $$ >> '` + pids + `'; '` + shimGit + `' "$@"`,
	} {
		wrapDir := filepath.Join(root, "wrap-"+name)
		writeFakeTool(t, wrapDir, "git", body)
		run := func(path string, args ...string) (string, int) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, shimGit, args...)
			cmd.Env = []string{"PATH=" + path}
			out, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s: shim looped until timeout: %s", name, out)
			}
			code := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				code = exitErr.ExitCode()
			}
			return strings.TrimSpace(string(out)), code
		}
		for _, args := range [][]string{{"status"}, {"remote", "-v"}, {"config", "user.name"}} {
			if name == "twice" && args[0] != "status" {
				continue // fans out 2^cap processes per run; one command shows it is bounded
			}
			if name != "twice" {
				out, code := run(dir+":"+wrapDir+":"+realDir, args...)
				if name == "exec" && args[0] == "status" {
					// An exec wrapper reaches the real git; the other routes stop at the hop cap.
					if code != 0 || out != "WRAP REAL status" {
						t.Errorf("exec wrapper with a real git behind it: %q (exit %d)", out, code)
					}
				} else if code != 127 || !strings.Contains(out, "git not found") {
					t.Errorf("%s %v with a real git behind the wrapper: %q (exit %d)", name, args, out, code)
				}
			}
			if out, code := run(dir+":"+wrapDir, args...); code != 127 || !strings.Contains(out, "git not found") {
				t.Errorf("%s %v with only the wrapper: %q (exit %d)", name, args, out, code)
			}
		}
		raw, _ := os.ReadFile(pids)
		for _, field := range strings.Fields(string(raw)) {
			pid, convErr := strconv.Atoi(field)
			proc, _ := os.FindProcess(pid)
			if convErr == nil && proc.Signal(syscall.Signal(0)) == nil {
				t.Errorf("%s: process %d left behind", name, pid)
			}
		}
	}
}
