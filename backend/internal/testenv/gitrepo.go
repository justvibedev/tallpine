package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// GitRepo makes a repository with one commit on main and an origin remote at
// https://example.com/o/r.git, and returns its symlink-resolved path. It runs
// git with GIT_DIR, GIT_INDEX_FILE and GIT_WORK_TREE removed so it works while
// a test has set them to prove a service ignores them.
func GitRepo(t testing.TB) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	env := gitFixtureEnv()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"remote", "add", "origin", "https://example.com/o/r.git"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:forbidigo // test fixture; process imports testenv in its own tests
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func gitFixtureEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		switch {
		case hasEnvName(entry, "GIT_DIR"), hasEnvName(entry, "GIT_INDEX_FILE"), hasEnvName(entry, "GIT_WORK_TREE"):
		default:
			env = append(env, entry)
		}
	}
	return append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
}

// AgentWorktree is a linked worktree with extensions.worktreeConfig on, as in
// every repository Tallpine manages, and a script that records each run: what a
// fenced agent plants in its own config.worktree to reach the daemon's git.
type AgentWorktree struct {
	Repo, Path, Script, Marker string
}

// NewAgentWorktree builds the worktree and script.
func NewAgentWorktree(t testing.TB) AgentWorktree {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := AgentWorktree{
		Repo: GitRepo(t), Path: filepath.Join(root, "w"),
		Script: filepath.Join(root, "agent.sh"), Marker: filepath.Join(root, "marker"),
	}
	if err := os.WriteFile(w.Script, []byte("#!/bin/sh\necho ran >> "+w.Marker+"\ncat\n"), 0o755); err != nil { //nolint:gosec // the fixture script must be executable
		t.Fatal(err)
	}
	w.Git(t, w.Repo, "config", "extensions.worktreeConfig", "true")
	w.Git(t, w.Repo, "worktree", "add", "-q", w.Path, "-b", "w")
	return w
}

// Git runs git in dir and fails the test if it does.
func (w AgentWorktree) Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := w.command(dir, args).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// TryGit runs git in dir for its side effects; a non-zero exit is expected for
// commands such as check-ignore.
func (w AgentWorktree) TryGit(dir string, args ...string) {
	_ = w.command(dir, args).Run()
}

func (w AgentWorktree) command(dir string, args []string) *exec.Cmd {
	cmd := exec.Command("git", args...) //nolint:forbidigo // test fixture; process imports testenv in its own tests
	cmd.Dir = dir
	cmd.Env = gitFixtureEnv()
	return cmd
}

// Ran reports whether the script ran since the last call, and clears the marker.
func (w AgentWorktree) Ran() bool {
	_, err := os.Stat(w.Marker)
	_ = os.Remove(w.Marker)
	return err == nil
}

// IgnoredRepoEnv points git's repository variables at a place with no
// repository, as a leaking session environment would.
func IgnoredRepoEnv(t testing.TB) {
	t.Helper()
	other := t.TempDir()
	t.Setenv("GIT_DIR", filepath.Join(other, "nope.git"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, "index"))
	t.Setenv("GIT_WORK_TREE", other)
}

func hasEnvName(entry, name string) bool {
	return len(entry) > len(name) && entry[:len(name)] == name && entry[len(name)] == '='
}
