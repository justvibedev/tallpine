package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func branchWorkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// branchWorkRepo makes a worktree on tp/w/root, clean and level with origin/main.
func branchWorkRepo(t *testing.T) (repo string, rec domain.SessionRecord) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo = filepath.Join(root, "work")
	branchWorkGit(t, root, "init", "--bare", "-b", "main", origin)
	branchWorkGit(t, root, "clone", origin, repo)
	branchWorkGit(t, repo, "symbolic-ref", "HEAD", "refs/heads/main")
	branchWorkGit(t, repo, "commit", "--allow-empty", "-m", "base")
	branchWorkGit(t, repo, "push", "origin", "main")
	branchWorkGit(t, repo, "switch", "-c", "tp/w/root")
	rec = domain.SessionRecord{ID: "w", ProjectID: "p", Kind: domain.KindWorker}
	rec.Metadata.WorkspacePath = repo
	rec.Metadata.Branch = "tp/w/root"
	rec.Metadata.DiffBaseRef = "main"
	return repo, rec
}

func TestWorkerBranchWork_CleanBranchHasNoWork(t *testing.T) {
	_, rec := branchWorkRepo(t)
	if got := (&Service{}).WorkerBranchWork(context.Background(), rec).KeepReason(); got != "" {
		t.Fatalf("clean branch kept: %s", got)
	}
}

// B588: unmerged local commits keep the worker.
func TestWorkerBranchWork_UnmergedLocalCommits(t *testing.T) {
	repo, rec := branchWorkRepo(t)
	branchWorkGit(t, repo, "commit", "--allow-empty", "-m", "work")
	got := (&Service{}).WorkerBranchWork(context.Background(), rec)
	if len(got.Unmerged) == 0 || !strings.Contains(got.KeepReason(), "heads/tp/w/root") {
		t.Fatalf("work = %+v", got)
	}
}

// B588, the dpf-4 shape: the branch was pushed and has not merged, even though
// no PR is tracked anywhere and the local branch has since been moved back.
func TestWorkerBranchWork_PushedUnmergedBranch(t *testing.T) {
	repo, rec := branchWorkRepo(t)
	branchWorkGit(t, repo, "commit", "--allow-empty", "-m", "work")
	branchWorkGit(t, repo, "push", "origin", "tp/w/root")
	branchWorkGit(t, repo, "reset", "--hard", "origin/main")
	got := (&Service{}).WorkerBranchWork(context.Background(), rec)
	if !strings.Contains(got.KeepReason(), "remotes/origin/tp/w/root") {
		t.Fatalf("work = %+v, want the pushed branch named", got)
	}
}

// B588: a sibling PR branch under the session namespace counts as its work.
func TestWorkerBranchWork_SiblingNamespaceBranch(t *testing.T) {
	repo, rec := branchWorkRepo(t)
	branchWorkGit(t, repo, "switch", "-c", "tp/w/topic")
	branchWorkGit(t, repo, "commit", "--allow-empty", "-m", "topic")
	branchWorkGit(t, repo, "switch", "tp/w/root")
	got := (&Service{}).WorkerBranchWork(context.Background(), rec)
	if !strings.Contains(got.KeepReason(), "heads/tp/w/topic") {
		t.Fatalf("work = %+v, want the sibling branch named", got)
	}
}

// Another session's branch in the shared repository is not this worker's work.
func TestWorkerBranchWork_IgnoresOtherSessionsBranches(t *testing.T) {
	repo, rec := branchWorkRepo(t)
	other := filepath.Join(t.TempDir(), "other")
	branchWorkGit(t, repo, "worktree", "add", "-b", "tp/other/root", other)
	branchWorkGit(t, other, "commit", "--allow-empty", "-m", "other")
	if got := (&Service{}).WorkerBranchWork(context.Background(), rec).KeepReason(); got != "" {
		t.Fatalf("kept on another session's branch: %s", got)
	}
}

// B588 review: a branch outside the namespace that the worktree created,
// pushed and left still holds this worker's unmerged work.
func TestWorkerBranchWork_SwitchedAwayBranch(t *testing.T) {
	repo, rec := branchWorkRepo(t)
	branchWorkGit(t, repo, "switch", "-c", "fix/foo")
	branchWorkGit(t, repo, "commit", "--allow-empty", "-m", "foo")
	branchWorkGit(t, repo, "push", "origin", "fix/foo")
	branchWorkGit(t, repo, "switch", "tp/w/root")
	got := (&Service{}).WorkerBranchWork(context.Background(), rec).KeepReason()
	if !strings.Contains(got, "heads/fix/foo") || !strings.Contains(got, "remotes/origin/fix/foo") {
		t.Fatalf("reason = %q, want local and pushed fix/foo named", got)
	}
}

// B588 review: an unreadable reflog cannot rule out a branch the worktree left.
func TestWorkerBranchWork_UnreadableReflogIsUnknown(t *testing.T) {
	_, rec := branchWorkRepo(t)
	prev := workspaceVisitedBranches
	workspaceVisitedBranches = func(context.Context, string) (map[string]struct{}, bool) { return nil, false }
	t.Cleanup(func() { workspaceVisitedBranches = prev })
	if got := (&Service{}).WorkerBranchWork(context.Background(), rec); !strings.Contains(got.Unknown, "reflog") {
		t.Fatalf("work = %+v, want Unknown naming the reflog", got)
	}
}

// B588: state that cannot be read keeps the worker.
func TestWorkerBranchWork_UnknownStates(t *testing.T) {
	_, rec := branchWorkRepo(t)
	noBase := rec
	noBase.Metadata.DiffBaseRef = "does-not-exist"
	noPath := rec
	noPath.Metadata.WorkspacePath = ""
	notGit := rec
	notGit.Metadata.WorkspacePath = t.TempDir()
	for name, r := range map[string]domain.SessionRecord{"no base": noBase, "no path": noPath, "not git": notGit} {
		if got := (&Service{}).WorkerBranchWork(context.Background(), r); got.Unknown == "" {
			t.Errorf("%s: %+v, want Unknown", name, got)
		}
	}
}
