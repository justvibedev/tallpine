package sessioncommit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeSessions map[domain.SessionID][2]string // workspace, repo

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	ws, ok := f[id]
	if !ok {
		return domain.SessionRecord{}, false, nil
	}
	return domain.SessionRecord{ID: id, Metadata: domain.SessionMetadata{WorkspacePath: ws[0], WorkspaceRepoPath: ws[1]}}, true, nil
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWithWorktree returns a main checkout and a linked worktree, the layout
// whose index sits in the main checkout's .git (the B11 cause).
func repoWithWorktree(t *testing.T) (main, wt string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	main = filepath.Join(root, "main")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "main")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	wt = filepath.Join(root, "wt")
	git(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	return main, wt
}

func newSvc(sessions Sessions) *Service {
	s := New(sessions, nil)
	return s
}

func TestCommitStagesAndCommitsInTheSessionsWorktree(t *testing.T) {
	// userconfig comes from HOME; give git an identity there.
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main, wt := repoWithWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newSvc(fakeSessions{"s1": {wt, main}})
	res, err := s.Commit(context.Background(), "s1", "add a")
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, wt, "rev-parse", "HEAD"); got != res.SHA {
		t.Fatalf("HEAD %s != result %s", got, res.SHA)
	}
	if got := git(t, wt, "log", "-1", "--format=%s"); got != "add a" {
		t.Fatalf("subject = %q", got)
	}
	if got := git(t, main, "log", "-1", "--format=%s"); got != "init" {
		t.Fatalf("main checkout was touched: %q", got)
	}
}

func TestCommitRefusals(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	main, wt := repoWithWorktree(t)
	sub := filepath.Join(wt, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newSvc(fakeSessions{"ok": {wt, main}, "sub": {sub, main}, "none": {"", main}, "rel": {"relative/dir", main}, "norepo": {wt, ""}})
	ctx := context.Background()
	cases := []struct {
		name, id, msg string
		want          error
	}{
		{"unknown session", "nope", "m", ErrSessionNotFound},
		{"empty message", "ok", "  ", ErrBadMessage},
		{"huge message", "ok", strings.Repeat("x", MaxMessageBytes+1), ErrBadMessage},
		{"no workspace", "none", "m", ErrNoWorkspace},
		{"relative workspace", "rel", "m", ErrNoWorkspace},
		{"subdirectory is not a worktree root", "sub", "m", ErrNotOwnWorktree},
		{"no recorded repo", "norepo", "m", ErrNotOwnWorktree},
		{"nothing to commit", "ok", "m", ErrNothingToCommit},
	}
	for _, c := range cases {
		if _, err := s.Commit(ctx, domain.SessionID(c.id), c.msg); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
}

// A message that looks like a flag must stay a message, and hooks must not run.
func TestCommitDoesNotRunHooksOrParseMessageAsFlags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main, wt := repoWithWorktree(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(main, ".git", "hooks")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	for _, h := range []string{"pre-commit", "commit-msg", "post-commit"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(script), 0o700); err != nil { //nolint:gosec // test hook
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSvc(fakeSessions{"s": {wt, main}}).Commit(context.Background(), "s", "--amend --no-edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repository hook ran outside the sandbox")
	}
	if got := git(t, wt, "log", "-1", "--format=%s"); got != "--amend --no-edit" {
		t.Fatalf("subject = %q", got)
	}
}

// The worktree's .git file is worker-controlled: pointing it at a git dir whose
// config defines a clean filter must not run the filter in the daemon.
func TestCommitIgnoresAWorkerRewrittenGitFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main, wt := repoWithWorktree(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	evil := filepath.Join(wt, "evil-git")
	git(t, wt, "init", "-q", "--separate-git-dir", evil, filepath.Join(t.TempDir(), "scratch"))
	cfg := "[core]\n\trepositoryformatversion = 0\n\tbare = false\n[filter \"x\"]\n\tclean = touch " + marker + "; cat\n"
	if err := os.WriteFile(filepath.Join(evil, "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+evil+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".gitattributes"), []byte("* filter=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "c.txt"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newSvc(fakeSessions{"s": {wt, main}}).Commit(context.Background(), "s", "x")
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a worker-defined filter ran in the daemon")
	}
	_ = err // refusal or a commit in the real git dir are both safe; running the filter is not
}

func gitIdentity(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte("[user]\n\tname = t\n\temail = t@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A linked worktree whose back-pointer is relative (worktree.useRelativePaths)
// must resolve; the absolute-only comparison refused it.
func TestCommitResolvesRelativeWorktreePointer(t *testing.T) {
	gitIdentity(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	main, wt := filepath.Join(root, "main"), filepath.Join(root, "wt")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "main")
	git(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	// Rewrite both pointers as relative paths by hand (what
	// `git worktree add --relative-paths` produces), so the test does not
	// need git >= 2.48.
	admin := filepath.Join(main, ".git", "worktrees", "wt")
	relGit, err := filepath.Rel(wt, admin)
	if err != nil {
		t.Fatal(err)
	}
	relBack, err := filepath.Rel(admin, filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+relGit+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(admin, "gitdir"), []byte(relBack+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSvc(fakeSessions{"s": {wt, main}}).Commit(context.Background(), "s", "m"); err != nil {
		t.Fatalf("relative pointer: %v", err)
	}
}

// A main checkout whose .git is a file (separate git dir) must resolve.
func TestCommitResolvesSeparateGitDirCheckout(t *testing.T) {
	gitIdentity(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	main, wt, sep := filepath.Join(root, "main"), filepath.Join(root, "wt"), filepath.Join(root, "sep.git")
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, main, "init", "-q", "-b", "main", "--separate-git-dir", sep)
	git(t, main, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSvc(fakeSessions{"s": {wt, main}}).Commit(context.Background(), "s", "m"); err != nil {
		t.Fatalf("separate git dir: %v", err)
	}
}
