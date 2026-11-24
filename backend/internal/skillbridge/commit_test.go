package skillbridge

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
	branch, err := PushAsBranch(context.Background(), checkout, "origin", res.SHA, nil)
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

func TestCommitPathsRefusesUnlistedPaths(t *testing.T) {
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
	if got := strings.TrimSpace(git(t, checkout, "rev-parse", "main")); got != before {
		t.Fatal("a refused commit moved main")
	}
}

// Commit pushes and PR-branch pushes run as the project's account: GIT_TRACE
// from the credential env reaches real git.
func TestPushesPassCredentialEnvToGit(t *testing.T) {
	checkout, origin := remoteRepo(t)
	trace := filepath.Join(t.TempDir(), "trace")
	target := originMain
	target.PushEnv = []string{"GIT_TRACE=" + trace}
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")
	res, err := CommitPaths(context.Background(), checkout, target, []string{"CLAUDE.md"}, []string{"CLAUDE.md"}, CommitMessage)
	if err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if b, err := os.ReadFile(trace); err != nil || !strings.Contains(string(b), "push") {
		t.Fatalf("commit push ran without the account: %v %s", err, b)
	}
	if err := os.Remove(trace); err != nil {
		t.Fatal(err)
	}
	branch, err := PushAsBranch(context.Background(), checkout, "origin", res.SHA, []string{"GIT_TRACE=" + trace})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(trace); err != nil || !strings.Contains(string(b), "push") {
		t.Fatalf("branch push ran without the account: %v %s", err, b)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "refs/heads/"+branch)); got != res.SHA {
		t.Fatalf("branch %s = %s", branch, got)
	}
}

// The owner's case: the dialog listed skill dirs the session branch already
// had. Those are skipped (never overwritten) and the rest is still committed.
func TestCommitPathsSkipsPathsTheTargetAlreadyHas(t *testing.T) {
	checkout, origin := remoteRepo(t)
	write(t, filepath.Join(checkout, "AGENTS.md"), "changed\n") // on the branch: must not be overwritten
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")
	paths := []string{"AGENTS.md", "CLAUDE.md"}
	res, err := CommitPaths(context.Background(), checkout, originMain, paths, paths, CommitMessage)
	if err != nil || res.SHA == "" || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if strings.Join(res.Skipped, ",") != "AGENTS.md" {
		t.Fatalf("skipped = %v", res.Skipped)
	}
	if got := committedFiles(t, origin, res.SHA); strings.Join(got, ",") != "CLAUDE.md" {
		t.Fatalf("committed %v", got)
	}
	if got := git(t, origin, "show", "main:AGENTS.md"); got != "rules\n" {
		t.Fatalf("AGENTS.md on main = %q", got)
	}
}

// A skill directory the target has even in part is left as it is: adding the
// checkout's extra files would change a skill already published there.
func TestCommitPathsSkipsSkillDirPartlyOnTarget(t *testing.T) {
	checkout, origin := remoteRepo(t)
	skill(t, filepath.Join(checkout, ".claude", "skills"), "3d-lighting", "light")
	git(t, checkout, "add", ".claude/skills/3d-lighting/SKILL.md")
	git(t, checkout, "commit", "-q", "-m", "partial skill")
	git(t, checkout, "push", "-q", "origin", "HEAD:main")
	git(t, checkout, "fetch", "-q", "origin")
	write(t, filepath.Join(checkout, ".claude", "skills", "3d-lighting", "extra.md"), "local only\n")
	before := strings.TrimSpace(git(t, origin, "rev-parse", "main"))
	p := []string{".claude/skills/3d-lighting"}
	res, err := CommitPaths(context.Background(), checkout, originMain, p, p, CommitMessage)
	if err != nil || res.SHA != "" || strings.Join(res.Skipped, ",") != p[0] {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := strings.TrimSpace(git(t, origin, "rev-parse", "main")); got != before {
		t.Fatal("an all-skipped commit moved main")
	}
}

// A path whose parent is a file or link on the target would replace it.
func TestCommitPathsSkipsPathUnderAFileOnTarget(t *testing.T) {
	checkout, _ := remoteRepo(t)
	ctx := context.Background()
	// .claude is a file on the target; the checkout has a .claude directory.
	tree := strings.TrimSpace(git(t, checkout, "rev-parse", "origin/main^{tree}"))
	blob := strings.TrimSpace(gitIn(t, checkout, "x\n", "hash-object", "-w", "--stdin"))
	entries := git(t, checkout, "ls-tree", tree) + "100644 blob " + blob + "\t.claude\n"
	newTree := strings.TrimSpace(gitIn(t, checkout, entries, "mktree"))
	parent := strings.TrimSpace(git(t, checkout, "commit-tree", newTree, "-p", "origin/main", "-m", "file"))
	git(t, checkout, "update-ref", "refs/remotes/origin/main", parent)
	skill(t, filepath.Join(checkout, ".claude", "skills"), "qa", "qa")
	write(t, filepath.Join(checkout, "CLAUDE.md"), "@AGENTS.md\n")
	p := []string{".claude/skills/qa", "CLAUDE.md"}
	res, err := CommitPaths(ctx, checkout, CommitTarget{Remote: "origin", Branch: "main", BaseRef: "refs/remotes/origin/main"}, p, p, CommitMessage)
	if err != nil || res.SHA == "" || strings.Join(res.Skipped, ",") != ".claude/skills/qa" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := strings.TrimSpace(git(t, checkout, "cat-file", "-t", res.SHA+":.claude")); got != "blob" {
		t.Fatalf(".claude on the new commit is a %s, want the target's file kept", got)
	}
}

// gitIn runs git with stdin.
func gitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
