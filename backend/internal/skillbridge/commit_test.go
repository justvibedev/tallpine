package skillbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// remoteRepo builds a bare origin with one commit on main and a clone of it
// (the project's main checkout).
func remoteRepo(t *testing.T) (checkout, origin string) {
	t.Helper()
	gitIdentity(t)
	base := t.TempDir()
	origin = filepath.Join(base, "origin.git")
	git(t, base, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(base, "seed")
	git(t, base, "clone", "-q", origin, seed)
	write(t, filepath.Join(seed, "AGENTS.md"), "rules\n")
	write(t, filepath.Join(seed, "app.txt"), "v1\n")
	git(t, seed, "add", "-A")
	git(t, seed, "commit", "-q", "-m", "init")
	git(t, seed, "push", "-q", "origin", "HEAD:main")
	checkout = filepath.Join(base, "checkout")
	git(t, base, "clone", "-q", origin, checkout)
	return checkout, origin
}

// gitIdentity gives CommitPaths' commit-tree an author in a scrubbed env.
func gitIdentity(t *testing.T) {
	t.Helper()
	for _, k := range []string{"GIT_AUTHOR_NAME", "GIT_COMMITTER_NAME"} {
		t.Setenv(k, "t")
	}
	for _, k := range []string{"GIT_AUTHOR_EMAIL", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(k, "t@t")
	}
}

var originMain = CommitTarget{Remote: "origin", Branch: "main", BaseRef: "refs/remotes/origin/main"}

func committedFiles(t *testing.T, dir, sha string) []string {
	t.Helper()
	out := strings.TrimSpace(git(t, dir, "diff-tree", "--no-commit-id", "--name-only", "-r", sha+"^", sha))
	return strings.Fields(out)
}

func TestCommitPathsCommitsOnlyCreatedPaths(t *testing.T) {
	checkout, origin := remoteRepo(t)
	// Unrelated dirty state: a modified tracked file, a staged new file, an untracked file.
	write(t, filepath.Join(checkout, "app.txt"), "local edit\n")
	write(t, filepath.Join(checkout, "staged.txt"), "staged\n")
	git(t, checkout, "add", "staged.txt")
	write(t, filepath.Join(checkout, "scratch.txt"), "untracked\n")
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")

	res, err := CommitPaths(context.Background(), checkout, originMain, []string{"CLAUDE.md"}, []string{"CLAUDE.md"}, CommitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if got := committedFiles(t, checkout, res.SHA); len(got) != 1 || got[0] != "CLAUDE.md" {
		t.Fatalf("commit touched %v, want only CLAUDE.md", got)
	}
	if !res.Pushed || res.PushError != "" || !res.LocalUpdated || res.SideBranch != "" {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "refs/heads/main")); got != res.SHA {
		t.Fatalf("origin main = %s, want %s", got, res.SHA)
	}
	if msg := strings.TrimSpace(git(t, checkout, "log", "-1", "--format=%s", res.SHA)); msg != CommitMessage {
		t.Fatalf("message = %q", msg)
	}
	// The checkout keeps its own changes: app.txt modified, staged.txt still
	// staged (not committed), scratch.txt untracked, CLAUDE.md clean.
	status := git(t, checkout, "status", "--porcelain")
	for _, want := range []string{" M app.txt", "A  staged.txt", "?? scratch.txt"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lost %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "CLAUDE.md") {
		t.Fatalf("CLAUDE.md not clean after commit:\n%s", status)
	}
	if body, _ := os.ReadFile(filepath.Join(checkout, "app.txt")); string(body) != "local edit\n" {
		t.Fatalf("app.txt overwritten: %q", body)
	}
}

func TestCommitPathsDefaultBranchNotCheckedOut(t *testing.T) {
	checkout, origin := remoteRepo(t)
	git(t, checkout, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(checkout, "feature.txt"), "wip\n")
	git(t, checkout, "add", "feature.txt")
	git(t, checkout, "commit", "-q", "-m", "feature work")
	featureHead := strings.TrimSpace(git(t, checkout, "rev-parse", "HEAD"))
	parent := strings.TrimSpace(git(t, checkout, "rev-parse", "origin/main"))
	write(t, filepath.Join(checkout, ".claude/skills/x/SKILL.md"), "---\nname: x\n---\n")

	res, err := CommitPaths(context.Background(), checkout, originMain, []string{".claude/skills/x"}, []string{".claude/skills/x"}, CommitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", res.SHA+"^")); got != parent {
		t.Fatalf("parent = %s, want origin/main %s", got, parent)
	}
	if got := committedFiles(t, checkout, res.SHA); len(got) != 1 || got[0] != ".claude/skills/x/SKILL.md" {
		t.Fatalf("commit touched %v", got)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "HEAD")); got != featureHead {
		t.Fatalf("checked-out branch moved: %s", got)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "main")); got != res.SHA || !res.LocalUpdated {
		t.Fatalf("local main = %s (updated=%v), want %s", got, res.LocalUpdated, res.SHA)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "main")); got != res.SHA {
		t.Fatalf("origin main = %s", got)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "origin/main")); got != res.SHA {
		t.Fatalf("tracking ref not updated: %s", got)
	}
	// The created skill is still an untracked file of the feature checkout.
	if !strings.Contains(git(t, checkout, "status", "--porcelain"), "?? .claude/") {
		t.Fatal("working tree changed")
	}
}

func TestCommitPathsPushRejectedKeepsCommit(t *testing.T) {
	checkout, origin := remoteRepo(t)
	hook := filepath.Join(origin, "hooks", "pre-receive")
	write(t, hook, "#!/bin/sh\necho 'protected branch' >&2\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	before := strings.TrimSpace(git(t, origin, "rev-parse", "main"))
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")

	res, err := CommitPaths(context.Background(), checkout, originMain, []string{"CLAUDE.md"}, []string{"CLAUDE.md"}, CommitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || !strings.Contains(res.PushError, "protected branch") {
		t.Fatalf("push result = %+v", res)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "main")); got != before {
		t.Fatal("remote main changed")
	}
	// The remote refused it: local main must not move ahead of the remote; the
	// commit is kept on the side branch.
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "main")); got != before || res.LocalUpdated {
		t.Fatalf("local main moved to %s (updated=%v) although the push was refused", got, res.LocalUpdated)
	}
	if res.SideBranch == "" {
		t.Fatal("refused commit not kept on a side branch")
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", res.SideBranch)); got != res.SHA {
		t.Fatalf("side branch = %s, want %s", got, res.SHA)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "origin/main")); got != before {
		t.Fatal("tracking ref moved although the push failed")
	}

	// Fallback: push the kept commit as a branch for a pull request.
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	branch, err := PushAsBranch(context.Background(), checkout, "origin", res.SHA)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "refs/heads/"+branch)); got != res.SHA {
		t.Fatalf("branch %s = %s", branch, got)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "main")); got != before {
		t.Fatal("fallback touched main")
	}
}

func TestCommitPathsKeepsCommitOnSideBranchWhenLocalDiverged(t *testing.T) {
	checkout, origin := remoteRepo(t)
	write(t, filepath.Join(origin, "hooks", "pre-receive"), "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(filepath.Join(origin, "hooks", "pre-receive"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(checkout, "local.txt"), "unpushed\n")
	git(t, checkout, "add", "local.txt")
	git(t, checkout, "commit", "-q", "-m", "unpushed local work")
	localHead := strings.TrimSpace(git(t, checkout, "rev-parse", "main"))
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")

	res, err := CommitPaths(context.Background(), checkout, originMain, []string{"CLAUDE.md"}, []string{"CLAUDE.md"}, CommitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if res.LocalUpdated || res.Pushed || res.SideBranch == "" {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "main")); got != localHead {
		t.Fatal("diverged local main was moved")
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", res.SideBranch)); got != res.SHA {
		t.Fatalf("side branch = %s", got)
	}
}

func TestCommitPathsLocalBaseDoesNotPush(t *testing.T) {
	gitIdentity(t)
	main, _ := repo(t, map[string]string{"AGENTS.md": "rules\n"})
	write(t, filepath.Join(main, "CLAUDE.md"), "@AGENTS.md\n")
	res, err := CommitPaths(context.Background(), main, CommitTarget{Remote: "origin", Branch: "main", BaseRef: "refs/heads/main"}, []string{"CLAUDE.md"}, []string{"CLAUDE.md"}, CommitMessage)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pushed || res.PushError != "" || !res.LocalUpdated {
		t.Fatalf("result = %+v", res)
	}
}

func TestCommitPathsRefusesUnlistedAndExistingPaths(t *testing.T) {
	checkout, _ := remoteRepo(t)
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")
	write(t, filepath.Join(checkout, "secret.txt"), "x\n")
	before := strings.TrimSpace(git(t, checkout, "rev-parse", "main"))
	ctx := context.Background()
	if _, err := CommitPaths(ctx, checkout, originMain, []string{"CLAUDE.md", "secret.txt"}, []string{"CLAUDE.md"}, CommitMessage); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("unlisted path: err = %v", err)
	}
	if _, err := CommitPaths(ctx, checkout, originMain, []string{"../x"}, []string{"../x"}, CommitMessage); !errors.Is(err, ErrPathNotAllowed) {
		t.Fatalf("escaping path: err = %v", err)
	}
	// AGENTS.md is on the branch already: committing the checkout's copy would overwrite it.
	write(t, filepath.Join(checkout, "AGENTS.md"), "changed\n")
	if _, err := CommitPaths(ctx, checkout, originMain, []string{"AGENTS.md"}, []string{"AGENTS.md"}, CommitMessage); !errors.Is(err, ErrPathExists) {
		t.Fatalf("existing path: err = %v", err)
	}
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "main")); got != before {
		t.Fatal("a refused commit moved main")
	}
}
