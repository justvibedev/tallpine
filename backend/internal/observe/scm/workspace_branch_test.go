package scm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func stubWorkspaceBranches(t *testing.T, byPath map[string]workspaceBranchInfo) {
	t.Helper()
	prev := readWorkspaceBranchesFunc
	readWorkspaceBranchesFunc = func(_ context.Context, path string) workspaceBranchInfo { return byPath[path] }
	t.Cleanup(func() { readWorkspaceBranchesFunc = prev })
}

func stubBranchCreatedLocally(t *testing.T, created bool) {
	t.Helper()
	prev := branchCreatedLocallyFunc
	branchCreatedLocallyFunc = func(context.Context, string, string) bool { return created }
	t.Cleanup(func() { branchCreatedLocallyFunc = prev })
}

func wsSession(id, path string) sessionRepo {
	return sessionRepo{session: domain.SessionRecord{ID: domain.SessionID(id), Metadata: domain.SessionMetadata{WorkspacePath: path}}, branch: "tp/" + id + "/root"}
}

func TestMatchWorkspaceBranch(t *testing.T) {
	visited := func(names ...string) map[string]struct{} {
		m := map[string]struct{}{}
		for _, n := range names {
			m[n] = struct{}{}
		}
		return m
	}
	for _, tc := range []struct {
		name  string
		infos map[string]workspaceBranchInfo
		cands []sessionRepo
		want  domain.SessionID
	}{
		{"checked out branch", map[string]workspaceBranchInfo{"/a": {current: "feat/x"}, "/b": {current: "other"}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-2", "/b")}, "p-1"},
		{"branch only visited", map[string]workspaceBranchInfo{"/a": {current: "main", visited: visited("feat/x", "main")}, "/b": {current: "other"}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-2", "/b")}, "p-1"},
		{"current beats visited", map[string]workspaceBranchInfo{"/a": {current: "feat/x"}, "/b": {current: "main", visited: visited("feat/x")}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-2", "/b")}, "p-1"},
		{"shared visited is ambiguous", map[string]workspaceBranchInfo{"/a": {visited: visited("feat/x")}, "/b": {visited: visited("feat/x")}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-2", "/b")}, ""},
		{"two sessions on branch is ambiguous", map[string]workspaceBranchInfo{"/a": {current: "feat/x"}, "/b": {current: "feat/x"}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-2", "/b")}, ""},
		{"same session scanned twice", map[string]workspaceBranchInfo{"/a": {current: "feat/x"}}, []sessionRepo{wsSession("p-1", "/a"), wsSession("p-1", "/a")}, "p-1"},
		{"unrelated branch", map[string]workspaceBranchInfo{"/a": {current: "other"}}, []sessionRepo{wsSession("p-1", "/a")}, ""},
		{"no workspace path", nil, []sessionRepo{wsSession("p-1", "")}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubWorkspaceBranches(t, tc.infos)
			stubBranchCreatedLocally(t, true)
			got, ok := matchWorkspaceBranch(context.Background(), tc.cands, "feat/x", map[string]workspaceBranchInfo{})
			if ok != (tc.want != "") || (ok && got.session.ID != tc.want) {
				t.Fatalf("got %q (%v), want %q", got.session.ID, ok, tc.want)
			}
		})
	}
	t.Run("workspace projects are skipped", func(t *testing.T) {
		stubWorkspaceBranches(t, map[string]workspaceBranchInfo{"/a": {current: "feat/x"}})
		stubBranchCreatedLocally(t, true)
		sr := wsSession("p-1", "/a")
		sr.workspace = true
		if _, ok := matchWorkspaceBranch(context.Background(), []sessionRepo{sr}, "feat/x", map[string]workspaceBranchInfo{}); ok {
			t.Fatal("workspace project session matched by worktree branch")
		}
	})
}

func TestPoll_DiscoversPRFromBranchOutsideSessionNamespace(t *testing.T) {
	stubWorkspaceBranches(t, map[string]workspaceBranchInfo{"/ws/p-1": {current: "topic/login-fix"}})
	stubBranchCreatedLocally(t, true)
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "tp/p-1/root"
	store.sessions[0].Metadata.WorkspacePath = "/ws/p-1"
	prObs := testObs(1)
	prObs.PR.SourceBranch = "topic/login-fix"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "topic/login-fix", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): prObs},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("PR from worktree branch not linked: %+v", store.writes)
	}
}

func TestPoll_DoesNotStealPRFromBranchOfAnotherSession(t *testing.T) {
	stubWorkspaceBranches(t, map[string]workspaceBranchInfo{
		"/ws/p-1": {current: "tp/p-1/root"},
		"/ws/p-2": {current: "topic/other"},
	})
	stubBranchCreatedLocally(t, true)
	store := testStoreWithSession()
	store.sessions[0].Metadata.Branch = "tp/p-1/root"
	store.sessions[0].Metadata.WorkspacePath = "/ws/p-1"
	prObs := testObs(1)
	prObs.PR.SourceBranch = "topic/other"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		openPRs: map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {
			{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "topic/other", HeadRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"},
		}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): prObs},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 0 {
		t.Fatalf("PR from a branch p-1's worktree never used was linked: %+v", store.writes)
	}
}

func TestMatchWorkspaceBranchRejectsBranchNotCreatedByWorktree(t *testing.T) {
	stubWorkspaceBranches(t, map[string]workspaceBranchInfo{"/a": {current: "feat/x"}})
	stubBranchCreatedLocally(t, false)
	if got, ok := matchWorkspaceBranch(context.Background(), []sessionRepo{wsSession("p-1", "/a")}, "feat/x", map[string]workspaceBranchInfo{}); ok {
		t.Fatalf("linked a branch the worktree only checked out: %q", got.session.ID)
	}
}

func TestReadWorkspaceBranchesFromRealWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "init")
	run("switch", "-q", "-c", "topic/a")
	run("switch", "-q", "main")
	info := readWorkspaceBranches(context.Background(), dir)
	if info.current != "main" {
		t.Fatalf("current = %q", info.current)
	}
	if _, ok := info.visited["topic/a"]; !ok {
		t.Fatalf("visited = %v, want topic/a", info.visited)
	}
}

func TestBranchCreatedLocallyDistinguishesCreatedFromFetched(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "f")
	run("commit", "-q", "-m", "init")
	run("switch", "-q", "-c", "mine")
	// What `gh pr checkout` does for someone else's PR: fetch into a new local
	// branch, then check it out.
	run("fetch", "-q", ".", "main:theirs")
	run("switch", "-q", "theirs")
	// ...and for a same-repo PR: a tracking branch created from the remote ref
	// of the same name, which logs "Created from origin/teammate".
	run("remote", "add", "origin", ".")
	run("update-ref", "refs/remotes/origin/teammate", "HEAD")
	run("switch", "-q", "-c", "teammate", "--track", "origin/teammate")
	run("update-ref", "refs/remotes/origin/nested/teammate", "HEAD")
	run("switch", "-q", "-c", "nested/teammate", "--track", "origin/nested/teammate")
	// A branch authored here but started from a differently named remote ref is
	// still the session's own work.
	run("switch", "-q", "-c", "mine-from-remote", "origin/teammate")
	ctx := context.Background()
	if !branchCreatedLocally(ctx, dir, "mine") {
		t.Error("a branch made with switch -c was not recognised as created locally")
	}
	if branchCreatedLocally(ctx, dir, "theirs") {
		t.Error("a fetched branch was treated as authored by this worktree")
	}
	for _, theirs := range []string{"teammate", "nested/teammate"} {
		if branchCreatedLocally(ctx, dir, theirs) {
			t.Errorf("tracking branch %q copied from its remote was treated as authored here", theirs)
		}
	}
	if !branchCreatedLocally(ctx, dir, "mine-from-remote") {
		t.Error("a branch started from a differently named remote ref was rejected")
	}
	if branchCreatedLocally(ctx, dir, "does-not-exist") {
		t.Error("a missing branch was treated as created locally")
	}
}

// B588 review: a reflog git cannot read is reported, not treated as empty.
func TestWorkspaceVisitedBranches_ReportsUnreadableReflog(t *testing.T) {
	if _, ok := WorkspaceVisitedBranches(context.Background(), t.TempDir()); ok {
		t.Fatal("a non-repository must report an unreadable reflog")
	}
}
