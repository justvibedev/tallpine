package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

func TestClassifyGitWrite(t *testing.T) {
	for _, tc := range []struct {
		args string
		want gitWriteKind
	}{
		{"remote add x u", gitWriteRemote},
		{"remote set-url origin u", gitWriteRemote},
		{"remote rename a b", gitWriteRemote},
		{"remote remove a", gitWriteRemote},
		{"remote rm a", gitWriteRemote},
		{"remote set-branches origin m", gitWriteRemote},
		{"remote set-head origin -a", gitWriteRemote},
		{"remote -v add x u", gitWriteRemote},
		{"-C /a remote add x u", gitWriteRemote},
		{"-c a=b config foo.bar v", gitWriteSharedConfig},
		{"config foo.bar v", gitWriteSharedConfig},
		{"config --local foo.bar v", gitWriteSharedConfig},
		{"config --add foo.bar v", gitWriteSharedConfig},
		{"config --unset foo.bar", gitWriteSharedConfig},
		{"config --unset-all foo.bar", gitWriteSharedConfig},
		{"config --replace-all foo.bar v", gitWriteSharedConfig},
		{"config --rename-section a b", gitWriteSharedConfig},
		{"config --remove-section a", gitWriteSharedConfig},
		{"config --edit", gitWriteSharedConfig},
		{"config -e", gitWriteSharedConfig},
		{"config set foo.bar v", gitWriteSharedConfig},
		{"config unset foo.bar", gitWriteSharedConfig},
		{"config --type bool foo.bar true", gitWriteSharedConfig},

		{"remote", gitWriteNone},
		{"remote -v", gitWriteNone},
		{"remote show origin", gitWriteNone},
		{"remote get-url origin", gitWriteNone},
		{"remote update", gitWriteNone},
		{"remote prune origin", gitWriteNone},
		{"config foo.bar", gitWriteNone},
		{"config --get foo.bar", gitWriteNone},
		{"config --get-regexp x", gitWriteNone},
		{"config --get-urlmatch a b", gitWriteNone},
		{"config --list", gitWriteNone},
		{"config -l", gitWriteNone},
		{"config --local --type=bool --get ext.x", gitWriteNone},
		{"config get foo.bar", gitWriteNone},
		{"config list", gitWriteNone},
		{"config --worktree foo.bar v", gitWriteNone},
		{"config --global a.b c", gitWriteNone},
		{"config --file x a.b c", gitWriteNone},
		{"config -f x a.b c", gitWriteNone},
		{"config branch.feat.remote origin", gitWriteNone},
		{"push -u origin HEAD", gitWriteNone},
		{"branch --set-upstream-to=origin/main", gitWriteNone},
		{"commit -m remote", gitWriteNone},
	} {
		_, command := splitGitGlobals(strings.Fields(tc.args))
		if got := classifyGitWrite(command).kind; got != tc.want {
			t.Errorf("git %s: kind = %v, want %v", tc.args, got, tc.want)
		}
	}
}

type gitGuardFixture struct {
	t                     *testing.T
	owner, worktree, shim string
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A real owner checkout with a real linked worktree, as Tallpine provisions it.
func newGitGuardFixture(t *testing.T) gitGuardFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no sh shim on Windows")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")
	root := t.TempDir()
	owner, worktree := filepath.Join(root, "owner"), filepath.Join(root, "wt")
	if err := os.Mkdir(owner, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitT(t, owner, "init", "-q")
	runGitT(t, owner, "commit", "-q", "--allow-empty", "-m", "i")
	runGitT(t, owner, "remote", "add", "origin", "https://example.com/orig.git")
	runGitT(t, owner, "config", "extensions.worktreeConfig", "true")
	runGitT(t, owner, "worktree", "add", "-q", worktree, "-b", "work")
	t.Setenv(agentlaunch.EnvGitGuardRoot, owner)
	t.Setenv(agentlaunch.EnvAllowSharedGitConfig, "")
	return gitGuardFixture{t: t, owner: owner, worktree: worktree, shim: filepath.Join(root, "no-shim-here")}
}

func (f gitGuardFixture) exec(args ...string) (string, string, error) {
	f.t.Helper()
	setConfigEnv(f.t)
	full := append([]string{"git-exec", f.shim, "--"}, args...)
	return executeCLI(f.t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }}, full...)
}

func (f gitGuardFixture) ownerConfig() string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.owner, ".git", "config"))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(data)
}

// B600: the done-when. Every write a session could make from its linked worktree
// is refused and the owner's checkout config is byte-identical afterwards.
func TestGitExecLeavesTheOwnerCheckoutConfigUnchanged(t *testing.T) {
	f := newGitGuardFixture(t)
	before := f.ownerConfig()
	for _, args := range [][]string{
		{"remote", "set-url", "origin", "https://evil.example/x.git"},
		{"remote", "add", "extra", "https://evil.example/y.git"},
		{"remote", "rename", "origin", "other"},
		{"remote", "remove", "origin"},
		{"config", "user.name", "AGENT"},
		{"config", "--local", "user.name", "AGENT"},
		{"config", "--add", "foo.bar", "v"},
		{"config", "--unset", "remote.origin.url"},
	} {
		_, stderr, err := f.exec(append([]string{"-C", f.worktree}, args...)...)
		if ExitCode(err) != gitFatalExit || !strings.Contains(stderr, "refused") || !strings.Contains(stderr, agentlaunch.EnvAllowSharedGitConfig) {
			t.Errorf("%v: exit %d stderr %q, want a refusal naming the override", args, ExitCode(err), stderr)
		}
	}
	if after := f.ownerConfig(); after != before {
		t.Fatalf("owner config changed:\n%s\n---\n%s", before, after)
	}
	// The owner's own checkout is covered too (an orchestrator can run there).
	if _, stderr, err := f.exec("-C", f.owner, "remote", "set-url", "origin", "https://evil.example/x.git"); ExitCode(err) != gitFatalExit {
		t.Fatalf("write in the owner checkout: exit %d %q", ExitCode(err), stderr)
	}
	if !strings.Contains(runGitT(t, f.owner, "remote", "-v"), "orig.git") {
		t.Fatal("owner remote changed")
	}
}

func TestGitExecSuggestsWorktreeScopeAndAllowsIt(t *testing.T) {
	f := newGitGuardFixture(t)
	_, stderr, err := f.exec("-C", f.worktree, "config", "user.name", "AGENT")
	if ExitCode(err) != gitFatalExit || !strings.Contains(stderr, "--worktree") {
		t.Fatalf("exit %d stderr %q", ExitCode(err), stderr)
	}
	before := f.ownerConfig()
	if _, stderr, err := f.exec("-C", f.worktree, "config", "--worktree", "user.name", "AGENT"); err != nil {
		t.Fatalf("--worktree must pass: %v %s", err, stderr)
	}
	if got := runGitT(t, f.worktree, "config", "--get", "user.name"); got != "AGENT" {
		t.Fatalf("worktree value = %q", got)
	}
	if f.ownerConfig() != before {
		t.Fatal("--worktree reached the owner config")
	}
}

func TestGitExecPassesReadsAndBranchTrackingAndOtherRepos(t *testing.T) {
	f := newGitGuardFixture(t)
	for _, args := range [][]string{
		{"-C", f.worktree, "remote", "-v"},
		{"-C", f.worktree, "remote", "get-url", "origin"},
		{"-C", f.worktree, "config", "--get", "remote.origin.url"},
		{"-C", f.worktree, "config", "branch.work.remote", "origin"},
	} {
		if _, stderr, err := f.exec(args...); err != nil {
			t.Errorf("%v must pass: %v %s", args, err, stderr)
		}
	}
	scratch := t.TempDir()
	runGitT(t, scratch, "init", "-q")
	if _, stderr, err := f.exec("-C", scratch, "remote", "add", "origin", "https://example.com/s.git"); err != nil {
		t.Fatalf("scratch repos are not guarded: %v %s", err, stderr)
	}
	if _, stderr, err := f.exec("-C", scratch, "config", "user.name", "x"); err != nil {
		t.Fatalf("scratch repos are not guarded: %v %s", err, stderr)
	}
}

func TestGitExecOverrideIsTheOnlyWayThrough(t *testing.T) {
	f := newGitGuardFixture(t)
	t.Setenv(agentlaunch.EnvAllowSharedGitConfig, "1")
	if _, stderr, err := f.exec("-C", f.worktree, "remote", "set-url", "origin", "https://example.com/new.git"); err != nil {
		t.Fatalf("the override must let an owner-asked change through: %v %s", err, stderr)
	}
	if !strings.Contains(runGitT(t, f.owner, "remote", "-v"), "new.git") {
		t.Fatal("override did not reach git")
	}
}

// Scripts read git's exit status and stderr: a passthrough adds neither a
// different status nor a line of its own (main prints nothing for "").
func TestGitExecPassthroughKeepsGitsStatusAndAddsNoNoise(t *testing.T) {
	f := newGitGuardFixture(t)
	_, _, err := f.exec("-C", f.worktree, "config", "--get", "no.such-key")
	if ExitCode(err) != 1 || err.Error() != "" {
		t.Fatalf("exit %d, message %q; want git's 1 and nothing to print", ExitCode(err), err)
	}
}
