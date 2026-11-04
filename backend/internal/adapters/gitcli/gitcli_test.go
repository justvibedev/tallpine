package gitcli

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = Env(os.Environ())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "a.txt")
	run(t, dir, "commit", "-q", "-m", "init")
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestEnvScrubsRepositoryRedirects(t *testing.T) {
	in := []string{"PATH=/bin", "GIT_DIR=/x", "GIT_INDEX_FILE=/y", "GIT_WORK_TREE=/z", "GIT_DIRECTORY_X=keep", "HOME=/h"}
	got := strings.Join(Env(in), "\n")
	for _, bad := range []string{"GIT_DIR=", "GIT_INDEX_FILE=", "GIT_WORK_TREE="} {
		if strings.Contains(got, bad) {
			t.Fatalf("%s survived: %s", bad, got)
		}
	}
	for _, want := range []string{"PATH=/bin", "GIT_DIRECTORY_X=keep", "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never"} {
		if !strings.Contains(got, want) {
			t.Fatalf("%s missing: %s", want, got)
		}
	}
}

// A session environment that names another repository must not redirect the
// call away from the -C directory.
func TestOutputIgnoresInheritedGitDir(t *testing.T) {
	repo := newRepo(t)
	other := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(other, "nope.git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, "index"))
	t.Setenv("GIT_WORK_TREE", other)
	out, err := New().Output(context.Background(), repo, "ls-files")
	if err != nil {
		t.Fatalf("Output: %v", err)
	}
	if strings.TrimSpace(out) != "a.txt" {
		t.Fatalf("ls-files = %q, want a.txt", out)
	}
}

func TestOutputEnvAppliesCallerEnvAfterScrub(t *testing.T) {
	repo := newRepo(t)
	index := filepath.Join(t.TempDir(), "index")
	if _, err := New().OutputEnv(context.Background(), repo, []string{"GIT_INDEX_FILE=" + index}, "read-tree", "HEAD"); err != nil {
		t.Fatalf("read-tree: %v", err)
	}
	if _, err := os.Stat(index); err != nil {
		t.Fatalf("scratch index not written: %v", err)
	}
}

func TestTimeoutBoundsTheCall(t *testing.T) {
	repo := newRepo(t)
	r := &Runner{Timeout: time.Nanosecond}
	_, err := r.Output(context.Background(), repo, "log")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	var gitErr *ports.GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("error %T is not a GitError: %v", err, err)
	}
}

func TestOutputErrorCarriesDetail(t *testing.T) {
	repo := newRepo(t)
	_, err := New().Output(context.Background(), repo, "cat-file", "-e", "deadbeef^{commit}")
	var gitErr *ports.GitError
	if !errors.As(err, &gitErr) {
		t.Fatalf("want GitError, got %v", err)
	}
}

func TestOutputCappedKeepsPrefix(t *testing.T) {
	repo := newRepo(t)
	out, truncated, err := New().OutputCapped(context.Background(), repo, 3, "show", "HEAD:a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hel" || !truncated {
		t.Fatalf("got %q truncated=%v", out, truncated)
	}
	out, truncated, err = New().OutputCapped(context.Background(), repo, 100, "show", "HEAD:a.txt")
	if err != nil || out != "hello\n" || truncated {
		t.Fatalf("got %q truncated=%v err=%v", out, truncated, err)
	}
}

func TestTypedHelpers(t *testing.T) {
	repo := newRepo(t)
	run(t, repo, "remote", "add", "origin", "https://example.com/o/r.git")
	r := New()
	ctx := context.Background()
	if top, err := r.TopLevel(ctx, repo); err != nil || top != repo {
		t.Fatalf("TopLevel = %q, %v; want %q", top, err, repo)
	}
	if url, err := r.RemoteURL(ctx, repo, "origin"); err != nil || url != "https://example.com/o/r.git" {
		t.Fatalf("RemoteURL = %q, %v", url, err)
	}
	if _, err := r.RemoteURL(ctx, repo, "missing"); err == nil {
		t.Fatal("RemoteURL(missing) should fail")
	}
	if dir, err := r.CommonDir(ctx, repo); err != nil || dir != filepath.Join(repo, ".git") {
		t.Fatalf("CommonDir = %q, %v", dir, err)
	}
}

func TestRepoUnavailable(t *testing.T) {
	repo := newRepo(t)
	if RepoUnavailable(repo) {
		t.Fatal("live checkout reported unavailable")
	}
	wt := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone.git")
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+gone+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !RepoUnavailable(wt) {
		t.Fatal("worktree pointing at a deleted repository should be unavailable")
	}
	_, err := New().Output(context.Background(), wt, "status")
	if !errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		t.Fatalf("want ErrWorkspaceRepoUnavailable, got %v", err)
	}
	_, _, err = New().OutputCapped(context.Background(), wt, 10, "status")
	if !errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		t.Fatalf("capped: want ErrWorkspaceRepoUnavailable, got %v", err)
	}
}

// Plain keeps hooks and the user's git configuration (writes need them) but
// still drops the repository-redirecting variables.
func TestPlainRunsHooksAndScrubsEnv(t *testing.T) {
	repo := newRepo(t)
	hook := filepath.Join(repo, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "nope.git"))
	r := New()
	ctx := context.Background()
	if _, err := r.Run(ctx, repo, "commit", "--allow-empty", "-m", "x"); err == nil {
		t.Fatal("pre-commit hook did not run under Plain/Run")
	}
	if _, err := r.Output(ctx, repo, "commit", "--allow-empty", "-m", "x"); err != nil {
		t.Fatalf("hardened Output must skip hooks: %v", err)
	}
	cmd := r.Plain(ctx, "", "clone", "--", "a", "b")
	if got := strings.Join(cmd.Args, " "); got != "git clone -- a b" {
		t.Fatalf("clone argv = %q", got)
	}
	for _, entry := range cmd.Env {
		if strings.HasPrefix(entry, "GIT_DIR=") {
			t.Fatal("GIT_DIR survived Plain")
		}
	}
}

func TestIsolatedUsesExactlyTheGivenEnv(t *testing.T) {
	cmd := New().Isolated(context.Background(), "/work", []string{"A=1"}, "status")
	if cmd.Dir != "/work" || len(cmd.Env) != 1 || cmd.Env[0] != "A=1" {
		t.Fatalf("dir=%q env=%v", cmd.Dir, cmd.Env)
	}
	if got := strings.Join(cmd.Args, " "); got != "git status" {
		t.Fatalf("argv = %q", got)
	}
}

// A repository's own diff.external must never run for a hardened call, for
// every command that renders patches.
func TestHardenedCallsNeverRunConfiguredExternalDiff(t *testing.T) {
	repo := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "ext.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "config", "diff.external", script)
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "commit", "-q", "-am", "second")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := New()
	ctx := context.Background()
	for _, args := range [][]string{
		{"diff", "HEAD"},
		{"-c", "core.pager=cat", "diff", "--binary", "HEAD~1", "HEAD"},
		{"log", "-p", "-n", "2"},
		{"show", "HEAD"},
	} {
		out, err := r.Output(ctx, repo, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		if !strings.Contains(out, "+") {
			t.Fatalf("git %v produced no patch: %q", args, out)
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatalf("git %v ran the configured diff.external", args)
		}
	}
	if _, _, err := r.OutputCapped(ctx, repo, 1<<16, "diff", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("OutputCapped ran the configured diff.external")
	}
}
