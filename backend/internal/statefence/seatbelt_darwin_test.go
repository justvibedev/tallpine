//go:build darwin

package statefence

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
)

// TestSeatbeltProfileEnforced runs real commands under sandbox-exec with a
// generated profile against a scratch data dir.
func TestSeatbeltProfileEnforced(t *testing.T) {
	if !Available("darwin") {
		t.Skip("sandbox-exec not present")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not present")
	}
	f := newFenceEnv(t)
	for _, p := range []string{filepath.Join(f.dataDir, "ao.db"), daemontoken.Path(f.runFile)} {
		if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	repo := filepath.Join(f.root, "repo")
	ws := filepath.Join(f.root, "wt")
	gitEnv := append(os.Environ(), "HOME="+f.home, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	run := func(argv ...string) error {
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = gitEnv
		out, err := c.CombinedOutput()
		if err != nil {
			t.Logf("%q: %v\n%s", argv, err, out)
		}
		return err
	}
	if err := run("git", "init", "-q", repo); err != nil {
		t.Fatal(err)
	}
	if err := run("git", "-C", repo, "commit", "-q", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if err := run("git", "-C", repo, "worktree", "add", "-q", "-b", "wt", ws); err != nil {
		t.Fatal(err)
	}
	profile := mustProfile(t, f.env, protectOn(t, nil), ws, HarnessPaths{})
	fenced := func(argv ...string) error { return run(Wrap(argv, profile)...) }

	if fenced("cat", filepath.Join(f.dataDir, "ao.db")) == nil {
		t.Error("reading ao.db must fail")
	}
	if fenced("cat", daemontoken.Path(f.runFile)) == nil {
		t.Error("reading the install token must fail")
	}
	if err := fenced("touch", filepath.Join(ws, "ok")); err != nil {
		t.Error("write in worktree must succeed")
	}
	if err := fenced("touch", filepath.Join(f.tmp, "ok")); err != nil {
		t.Error("write in TMPDIR must succeed")
	}
	if fenced("touch", filepath.Join(f.home, "nope")) == nil {
		t.Error("write in HOME must fail")
	}
	if err := fenced("git", "-C", ws, "commit", "-q", "--allow-empty", "-m", "fenced"); err != nil {
		t.Error("git commit in a linked worktree must succeed")
	}
	if fenced("git", "-C", ws, "config", "--local", "x.y", "z") == nil {
		t.Error("writing the shared .git/config must fail")
	}
}
