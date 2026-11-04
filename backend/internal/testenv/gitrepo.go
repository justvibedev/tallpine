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
	var env []string
	for _, entry := range os.Environ() {
		switch {
		case hasEnvName(entry, "GIT_DIR"), hasEnvName(entry, "GIT_INDEX_FILE"), hasEnvName(entry, "GIT_WORK_TREE"):
		default:
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
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
