package cleanup

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type fakeBranchSources struct {
	projects  []domain.ProjectRecord
	prs       map[domain.SessionID][]domain.PullRequest
	worktrees map[domain.SessionID][]domain.SessionWorktreeRecord
	store     *fakeStore
}

func (f fakeBranchSources) ListSessionWorktrees(_ context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error) {
	return f.worktrees[id], nil
}

func (f fakeBranchSources) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	for _, rec := range f.store.sessions {
		if rec.ID == id {
			return rec, true, nil
		}
	}
	return domain.SessionRecord{}, false, nil
}

var branchNow = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

// prRemoteURL is origin's URL: the repository of the PRs below. forkURL is
// another repository on the same host. The fixture's fake ssh serves both.
const (
	prRemoteURL = "ssh://git@github.com/acme/repo.git"
	forkURL     = "ssh://git@github.com/me/fork.git"
)

// openGuard runs the deletion right away, as the session manager does when
// nothing in the project is restoring or spawning.
type openGuard struct{}

func (openGuard) GuardBranchCleanup(ctx context.Context, _ domain.SessionID, fn func(context.Context)) (string, error) {
	fn(ctx)
	return "", nil
}

// closedGuard refuses, as the session manager does while a restore holds the
// session's guard or the project's workspace gate.
type closedGuard struct {
	reason string
	err    error
}

func (g closedGuard) GuardBranchCleanup(context.Context, domain.SessionID, func(context.Context)) (string, error) {
	return g.reason, g.err
}

// branchesOn turns branch cleanup on; it is off by default.
const branchesOn = `{"branches":{"enabled":true}}`

func runBranchRules(t *testing.T, store Store, req RunRequest, rules ...Rule) Report {
	t.Helper()
	svc := New(store, rules, Config{Pause: -1, StartDelay: -1, Clock: func() time.Time { return branchNow }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	report, err := svc.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func (f fakeBranchSources) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return f.projects, nil
}

func (f fakeBranchSources) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs[id], nil
}

type gitFixture struct {
	t      *testing.T
	repo   string
	origin string
	server string // serves ssh://git@github.com/<path> from <server>/<path>
}

func (g *gitFixture) git(dir string, args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newGitFixture(t *testing.T) *gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the fake ssh server is a shell script")
	}
	g := &gitFixture{t: t, repo: filepath.Join(t.TempDir(), "repo"), server: t.TempDir()}
	// Remotes have real host URLs, so what git resolves them to (insteadOf,
	// pushurl) is compared as in production; this ssh serves them locally.
	ssh := filepath.Join(t.TempDir(), "ssh")
	script := "#!/bin/sh\nfor last; do :; done\nexec sh -c \"$(printf '%s' \"$last\" | sed \"s#'/#'" + g.server + "/#\")\"\n"
	if err := os.WriteFile(ssh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_SSH_COMMAND", ssh)
	g.origin = g.bare("acme/repo.git")
	g.git(filepath.Dir(g.repo), "init", "-q", "-b", "main", g.repo)
	g.git(g.repo, "commit", "-q", "--allow-empty", "-m", "init")
	g.git(g.repo, "remote", "add", "origin", prRemoteURL)
	g.git(g.repo, "push", "-q", "origin", "main")
	return g
}

// bare creates the server's repository at path and returns its directory.
func (g *gitFixture) bare(path string) string {
	g.t.Helper()
	dir := filepath.Join(g.server, path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		g.t.Fatal(err)
	}
	g.git(dir, "init", "-q", "--bare", "-b", "main", dir)
	return dir
}

// branch creates branch with one commit of its own off main and returns its tip.
func (g *gitFixture) branch(name string) string {
	g.t.Helper()
	g.git(g.repo, "checkout", "-q", "-b", name, "main")
	g.git(g.repo, "commit", "-q", "--allow-empty", "-m", name)
	tip := g.git(g.repo, "rev-parse", "HEAD")
	g.git(g.repo, "checkout", "-q", "main")
	return tip
}

func (g *gitFixture) exists(ref string) bool {
	cmd := exec.Command("git", "-C", g.repo, "rev-parse", "--verify", "--quiet", ref)
	return cmd.Run() == nil
}

func worker(id domain.SessionID, terminatedFor time.Duration) domain.SessionRecord {
	rec := domain.SessionRecord{ID: id, ProjectID: "mer", Kind: domain.KindWorker, UpdatedAt: branchNow}
	if terminatedFor > 0 {
		rec.IsTerminated = true
		rec.UpdatedAt = branchNow.Add(-terminatedFor)
	} else {
		// A live worker has its branch and workspace; one without them is
		// still starting.
		rec.Metadata.Branch, rec.Metadata.WorkspacePath = "vo/"+string(id)+"/root", "/ws/"+string(id)
	}
	return rec
}

func TestBranchRuleDeletesOnlyBranchesWhoseWorkIsSafe(t *testing.T) {
	g := newGitFixture(t)
	day := 24 * time.Hour

	// Merged by fast-forward into main.
	g.branch("vo/mer-1/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "main")
	// Squash-merged PR: the tip is the PR's head but not in main.
	squashTip := g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	// A merged PR whose branch got a commit after the merge.
	movedHead := g.branch("vo/mer-3/root")
	g.git(g.repo, "checkout", "-q", "vo/mer-3/root")
	g.git(g.repo, "commit", "-q", "--allow-empty", "-m", "after merge")
	g.git(g.repo, "checkout", "-q", "main")
	// Unpushed, unmerged work.
	g.branch("vo/mer-4/root")
	// Pushed but unmerged: one long ago, one recently.
	g.branch("vo/mer-5/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-5/root")
	g.branch("vo/mer-6/topic")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-6/topic")
	// Merged but checked out in a worktree.
	g.branch("vo/mer-7/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-7/root")
	g.git(g.repo, "worktree", "add", "-q", filepath.Join(t.TempDir(), "wt"), "vo/mer-7/root")
	// Merged, but the session has preserved uncommitted work.
	preservedTip := g.branch("vo/mer-8/root")
	g.git(g.repo, "update-ref", "refs/ao/preserved/mer-8", preservedTip)
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-8/root")
	// Merged, but the session is live.
	g.branch("vo/mer-9/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-9/root")
	// Branches Tallpine cannot attribute.
	g.branch("vo/unknown-1/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/unknown-1/root")
	g.branch("feature/mine")
	g.git(g.repo, "merge", "-q", "--ff-only", "feature/mine")
	// Merged recently: within the merged retention.
	g.branch("vo/mer-10/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-10/root")
	g.git(g.repo, "push", "-q", "origin", "main")

	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{
		worker("mer-1", 10*day), worker("mer-2", 10*day), worker("mer-3", 10*day), worker("mer-4", 400*day),
		worker("mer-5", 40*day), worker("mer-6", 10*day), worker("mer-7", 10*day), worker("mer-8", 10*day),
		worker("mer-9", 0), worker("mer-10", day),
	}}
	src := fakeBranchSources{
		store:    store,
		projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}},
		prs: map[domain.SessionID][]domain.PullRequest{
			"mer-2": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-2/root", HeadSHA: squashTip}},
			"mer-3": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-3/root", HeadSHA: movedHead}},
		},
	}
	rule := NewBranchRule(src, openGuard{}, nil)

	dry := runBranchRules(t, store, RunRequest{DryRun: true}, rule)
	if cat := dry.Categories[0]; cat.WouldFree != 3 {
		t.Fatalf("dry run would delete %d branches, want 3: %+v", cat.WouldFree, cat.Items)
	}
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("dry run deleted a branch")
	}

	report := runBranchRules(t, store, RunRequest{}, rule)
	if cat := report.Categories[0]; cat.Freed != 3 || cat.Failed != 0 {
		t.Fatalf("deleted %d (failed %d), want 3: %+v", cat.Freed, cat.Failed, cat.Items)
	}
	for _, gone := range []string{"vo/mer-1/root", "vo/mer-2/root", "vo/mer-5/root"} {
		if g.exists("refs/heads/" + gone) {
			t.Errorf("%s still present", gone)
		}
	}
	for _, kept := range []string{"vo/mer-3/root", "vo/mer-4/root", "vo/mer-6/topic", "vo/mer-7/root", "vo/mer-8/root", "vo/mer-9/root", "vo/unknown-1/root", "feature/mine", "vo/mer-10/root"} {
		if !g.exists("refs/heads/" + kept) {
			t.Errorf("%s deleted", kept)
		}
	}
	if !g.exists("refs/ao/preserved/mer-8") {
		t.Fatal("preserved ref deleted")
	}
	// Remote branches stay unless deleteRemote is on.
	if g.git(g.origin, "branch", "--list", "vo/mer-2/root") == "" {
		t.Fatal("remote branch deleted without deleteRemote")
	}
	reasons := map[domain.SessionID]string{}
	for _, item := range report.Categories[0].Items {
		reasons[item.SessionID] = item.Reason
	}
	for id, want := range map[domain.SessionID]string{
		"mer-3": "nowhere else", "mer-4": "nowhere else", "mer-6": "unmerged; deleted 30", "mer-7": "worktree",
		"mer-8": "preserved", "mer-10": "merged; deleted 7",
	} {
		if !strings.Contains(reasons[id], want) {
			t.Errorf("%s reason %q, want %q", id, reasons[id], want)
		}
	}
	if _, ok := reasons["mer-9"]; ok {
		t.Error("a live session's branch was considered")
	}
}

func TestBranchRuleDeletesTheRemoteBranchOfAMergedPROnlyWhenAskedAndUnmoved(t *testing.T) {
	g := newGitFixture(t)
	head := g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	other := g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	// The PR's record has no repository.
	unknown := g.branch("vo/mer-3/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-3/root")
	// A backup remote carries the same branch: it is not the PR's repository.
	backup := filepath.Join(t.TempDir(), "backup.git")
	g.git(filepath.Dir(backup), "init", "-q", "--bare", backup)
	g.git(g.repo, "remote", "add", "backup", backup)
	g.git(g.repo, "push", "-q", "backup", "vo/mer-1/root")
	// Someone pushed to mer-2's remote branch after the merge.
	g.git(g.repo, "checkout", "-q", "vo/mer-2/root")
	g.git(g.repo, "commit", "-q", "--allow-empty", "-m", "late")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	g.git(g.repo, "reset", "-q", "--hard", other)
	g.git(g.repo, "checkout", "-q", "main")
	// A background pass must not run project code: the hook is skipped.
	hook := filepath.Join(g.repo, ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 30*24*time.Hour), worker("mer-2", 30*24*time.Hour), worker("mer-3", 30*24*time.Hour)}}
	src := fakeBranchSources{
		store:    store,
		projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}},
		prs: map[domain.SessionID][]domain.PullRequest{
			"mer-1": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-1/root", HeadSHA: head}},
			"mer-2": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-2/root", HeadSHA: other}},
			"mer-3": {{Merged: true, SourceBranch: "vo/mer-3/root", HeadSHA: unknown}},
		},
	}
	runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if g.git(g.origin, "branch", "--list", "vo/mer-1/root") != "" {
		t.Fatal("merged PR's remote branch not deleted")
	}
	if g.git(backup, "branch", "--list", "vo/mer-1/root") == "" {
		t.Fatal("deleted the branch on a remote that is not the PR's repository")
	}
	if g.git(g.origin, "branch", "--list", "vo/mer-2/root") == "" {
		t.Fatal("a remote branch that moved past the merged PR was deleted")
	}
	if g.git(g.origin, "branch", "--list", "vo/mer-3/root") == "" {
		t.Fatal("deleted a remote branch although the PR's repository is unknown")
	}
}

// mergedBranchSource is a branch source for the fixture's repo as project "mer"
// where session id's PR is merged at head, on acme/repo's github.com remote.
func mergedBranchSource(g *gitFixture, store *fakeStore, id domain.SessionID, head string) fakeBranchSources {
	return fakeBranchSources{
		store:    store,
		projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}},
		prs:      map[domain.SessionID][]domain.PullRequest{id: {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/" + string(id) + "/root", HeadSHA: head}}},
	}
}

// restoredSources reports some sessions live again when re-read, after the
// run's snapshot still showed them terminated.
type restoredSources struct {
	fakeBranchSources
	restored map[domain.SessionID]bool
}

func (f restoredSources) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, found, err := f.fakeBranchSources.GetSession(ctx, id)
	if f.restored[id] {
		rec.IsTerminated = false
	}
	return rec, found, err
}

// Whatever keeps the local branch keeps the remote one: a session restored
// since the snapshot, or a branch checked out since.
func TestBranchRuleKeepsTheRemoteBranchWhenTheLocalOneIsKept(t *testing.T) {
	g := newGitFixture(t)
	restoredHead := g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	checkedOutHead := g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")

	store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 30*24*time.Hour), worker("mer-2", 30*24*time.Hour)}}
	src := restoredSources{
		fakeBranchSources: fakeBranchSources{
			store:    store,
			projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}},
			prs: map[domain.SessionID][]domain.PullRequest{
				"mer-1": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-1/root", HeadSHA: restoredHead}},
				"mer-2": {{Merged: true, Host: "github.com", Repo: "acme/repo", SourceBranch: "vo/mer-2/root", HeadSHA: checkedOutHead}},
			},
		},
		restored: map[domain.SessionID]bool{"mer-1": true},
	}
	// mer-2's branch is checked out right before the rule's re-check.
	checkout := filepath.Join(t.TempDir(), "wt")
	git := func(ctx context.Context, repo string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "worktree" && args[1] == "list" && !exists(checkout) {
			g.git(g.repo, "worktree", "add", "-q", checkout, "vo/mer-2/root")
		}
		return RunGit(ctx, repo, args...)
	}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, git))
	for _, name := range []string{"vo/mer-1/root", "vo/mer-2/root"} {
		if g.git(g.repo, "branch", "--list", name) == "" {
			t.Errorf("local branch %s deleted", name)
		}
		if g.git(g.origin, "branch", "--list", name) == "" {
			t.Errorf("remote branch %s deleted although the local one was kept", name)
		}
	}
	if report.Categories[0].Freed != 0 {
		t.Fatalf("freed %d items, want none: %+v", report.Categories[0].Freed, report.Categories[0].Items)
	}
}

// A remote-tracking ref is only a cache: a branch deleted on the server (a PR
// closed with "delete branch") but not yet pruned here proves nothing, so the
// local branch, now the only copy of its commits, is kept.
func TestBranchRuleKeepsAPushedBranchTheServerNoLongerHas(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	g.git(g.origin, "branch", "-q", "-D", "vo/mer-1/root")
	if !g.exists("refs/remotes/origin/vo/mer-1/root") {
		t.Fatal("fixture: the tracking ref should still be there")
	}

	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), worker("mer-2", 60*24*time.Hour)}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch whose remote copy is gone from the server")
	}
	if g.exists("refs/heads/vo/mer-2/root") {
		t.Fatal("a branch still on the server was not deleted")
	}
	for _, item := range report.Categories[0].Items {
		if item.What == "branch vo/mer-1/root" && !strings.Contains(item.Reason, "remote server") {
			t.Fatalf("reason = %q", item.Reason)
		}
	}

	// A server that cannot be reached keeps a branch it does have.
	g.branch("vo/mer-3/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-3/root")
	g.git(g.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	store.sessions = append(store.sessions, worker("mer-3", 60*24*time.Hour))
	runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-3/root") {
		t.Fatal("deleted a branch although the server could not be asked")
	}
}

// Merged on remote evidence alone counts only once the server confirms it: a
// tracking ref of a branch deleted on the server proves nothing.
func TestBranchRuleConfirmsARemoteMergeWithTheServer(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root:refs/heads/feature-x")
	g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root:refs/heads/feature-y")
	g.git(g.origin, "branch", "-q", "-D", "feature-x")
	if !g.exists("refs/remotes/origin/feature-x") {
		t.Fatal("fixture: the stale tracking ref should still be there")
	}
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), worker("mer-2", 60*24*time.Hour)}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch merged only into a branch the server no longer has")
	}
	if g.exists("refs/heads/vo/mer-2/root") {
		t.Fatal("a branch merged into a branch still on the server was not deleted")
	}
}

// A live session that names the branch, as its own or as its diff base, keeps it.
func TestBranchRuleKeepsABranchALiveSessionUses(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root:refs/heads/main")
	user := worker("mer-9", 0)
	user.IsTerminated = false
	user.Metadata.DiffBaseRef = "vo/mer-1/root"
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), user}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch a live session uses")
	}
	for _, item := range report.Categories[0].Items {
		if item.What == "branch vo/mer-1/root" && !strings.Contains(item.Reason, "mer-9") {
			t.Fatalf("reason = %q, want the live session named", item.Reason)
		}
	}
}

// Off by default: the rule reports what it would delete and deletes nothing.
func TestBranchRuleOnlyReportsWhileOff(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "main")
	head := g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	store := &fakeStore{policy: `{"branches":{"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), worker("mer-2", 60*24*time.Hour)}}
	src := mergedBranchSource(g, store, "mer-2", head)
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") || !g.exists("refs/heads/vo/mer-2/root") {
		t.Fatalf("deleted a branch while branch cleanup is off: %+v", report.Categories)
	}
	if g.git(g.origin, "branch", "--list", "vo/mer-2/root") == "" {
		t.Fatal("deleted a remote branch while branch cleanup is off")
	}
	cat := report.Categories[0]
	if cat.Freed != 0 || len(cat.Items) != 2 {
		t.Fatalf("items = %+v", cat.Items)
	}
	for _, item := range cat.Items {
		if item.Status != StatusNeedsConfirmation || !strings.Contains(item.Reason, "branches.enabled") {
			t.Fatalf("item = %+v, want needs_confirmation", item)
		}
	}
}

// claimingSources makes a session that uses mer-1's branch appear after the
// run's snapshot, before the deletion: a spawn still provisioning, with no
// worktree yet.
type claimingSources struct {
	fakeBranchSources
	claim domain.SessionRecord
}

func (f claimingSources) ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	f.store.mu.Lock()
	f.store.sessions = append(f.store.sessions, f.claim)
	f.store.mu.Unlock()
	return f.fakeBranchSources.ListPRsBySession(ctx, id)
}

// Sessions are re-read right before the deletion: one spawned onto the branch
// since the snapshot keeps it, and so does a live session whose child-repo
// worktree uses it.
func TestBranchRuleRechecksLiveUsersRightBeforeDeleting(t *testing.T) {
	g := newGitFixture(t)
	for _, name := range []string{"vo/mer-1/root", "vo/mer-2/root", "vo/mer-3/root"} {
		g.branch(name)
		g.git(g.repo, "merge", "-q", "--ff-only", name)
	}
	g.git(g.repo, "push", "-q", "origin", "main")
	claim := worker("mer-8", 0)
	claim.ProjectID = "other"
	claim.Metadata.Branch = "vo/mer-1/root"
	child := worker("mer-9", 0)
	child.ProjectID = "other"
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), worker("mer-2", 60*24*time.Hour), worker("mer-3", 60*24*time.Hour), child}}
	src := claimingSources{
		fakeBranchSources: fakeBranchSources{
			store:     store,
			projects:  []domain.ProjectRecord{{ID: "mer", Path: g.repo}},
			worktrees: map[domain.SessionID][]domain.SessionWorktreeRecord{"mer-9": {{SessionID: "mer-9", RepoName: "lib", Branch: "vo/mer-9/root", BaseRef: "vo/mer-2/root"}}},
		},
		claim: claim,
	}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch a session started using after the snapshot")
	}
	if !g.exists("refs/heads/vo/mer-2/root") {
		t.Fatal("deleted a branch a live session's child-repo worktree uses")
	}
	if g.exists("refs/heads/vo/mer-3/root") {
		t.Fatalf("an unused merged branch was not deleted: %+v", report.Categories[0].Items)
	}
}

// A session still being set up in the project may not have recorded its
// branch yet, so it keeps the project's branches for now.
func TestBranchRuleKeepsBranchesWhileASessionIsStartingInTheProject(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "main")
	starting := worker("mer-9", 0)
	starting.ProvisionState = domain.SessionProvisionProvisioning
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), starting}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch while a session is starting in the project")
	}
}

// A failure to list the remote branches is reported, not swallowed.
func TestBranchRuleReportsWhenTheRemoteBranchCannotBeChecked(t *testing.T) {
	g := newGitFixture(t)
	head := g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour)}}
	src := mergedBranchSource(g, store, "mer-1", head)
	git := func(ctx context.Context, repo string, args ...string) (string, error) {
		if len(args) >= 2 && args[0] == "for-each-ref" && args[1] == "--format=%(refname)%00%(objectname)" {
			return "", errors.New("for-each-ref failed")
		}
		return RunGit(ctx, repo, args...)
	}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, git))
	if g.git(g.origin, "branch", "--list", "vo/mer-1/root") == "" {
		t.Fatal("remote branch deleted although it could not be checked")
	}
	if cat := report.Categories[0]; cat.Failed != 1 {
		t.Fatalf("failed = %d, want the remote branch reported: %+v", cat.Failed, cat.Items)
	}
}

// Nothing is deleted unless the session manager's guard lets the deletion
// run: a restore in flight refuses it, a failed guard reports it, and without
// a guard the rule only keeps. The remote branch survives with the local one.
func TestBranchRuleDeletesOnlyUnderTheSessionManagersGuard(t *testing.T) {
	for name, guard := range map[string]BranchGuard{
		"restore in flight": closedGuard{reason: "a session of the project is being restored"},
		"guard error":       closedGuard{err: errors.New("store unavailable")},
		"no guard":          nil,
	} {
		t.Run(name, func(t *testing.T) {
			g := newGitFixture(t)
			head := g.branch("vo/mer-1/root")
			g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
			store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour)}}
			src := mergedBranchSource(g, store, "mer-1", head)
			report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, guard, nil))
			if !g.exists("refs/heads/vo/mer-1/root") {
				t.Fatal("local branch deleted outside the guard")
			}
			if g.git(g.origin, "branch", "--list", "vo/mer-1/root") == "" {
				t.Fatal("remote branch deleted outside the guard")
			}
			if cat := report.Categories[0]; cat.Freed != 0 || len(cat.Items) != 1 || cat.Items[0].Reason == "" {
				t.Fatalf("items = %+v", cat.Items)
			}
		})
	}
}

// The server is listed once per remote per project, a failure included: an
// unreachable remote costs one timeout, not one per branch, and keeps them all.
func TestBranchRuleListsEachRemoteOncePerProject(t *testing.T) {
	g := newGitFixture(t)
	var sessions []domain.SessionRecord
	for _, id := range []string{"mer-1", "mer-2", "mer-3"} {
		g.branch("vo/" + id + "/root")
		g.git(g.repo, "push", "-q", "origin", "vo/"+id+"/root")
		sessions = append(sessions, worker(domain.SessionID(id), 60*24*time.Hour))
	}
	g.git(g.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	store := &fakeStore{policy: branchesOn, sessions: sessions}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	lsRemote := 0
	git := func(ctx context.Context, repo string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "ls-remote" {
			lsRemote++
		}
		return RunGit(ctx, repo, args...)
	}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, git))
	if lsRemote != 1 {
		t.Fatalf("ls-remote ran %d times, want once for the project", lsRemote)
	}
	if cat := report.Categories[0]; cat.Freed != 0 || cat.Kept != 3 {
		t.Fatalf("items = %+v", cat.Items)
	}
}

// The listing is from the start of the project's pass: a branch deleted on
// the server since is caught by asking again right before deleting.
func TestBranchRuleAsksTheServerAgainRightBeforeDeleting(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour)}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	git := func(ctx context.Context, repo string, args ...string) (string, error) {
		out, err := RunGit(ctx, repo, args...)
		if len(args) > 1 && args[0] == "ls-remote" && args[1] == "--heads" {
			// The PR is closed with "delete branch" right after the listing.
			g.git(g.origin, "branch", "-q", "-D", "vo/mer-1/root")
		}
		return out, err
	}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, git))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch the server no longer has")
	}
	if item := report.Categories[0].Items[0]; !strings.Contains(item.Reason, "no longer has") {
		t.Fatalf("item = %+v", item)
	}
}

// Another local branch is no evidence: merged only into a local branch (a
// scratch branch, unpushed main), the commits may exist nowhere on the server.
func TestBranchRuleKeepsABranchMergedOnlyLocally(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "merge", "-q", "--ff-only", "vo/mer-1/root")
	store := &fakeStore{policy: branchesOn, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour)}}
	src := fakeBranchSources{store: store, projects: []domain.ProjectRecord{{ID: "mer", Path: g.repo}}}
	report := runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch merged only into a local branch")
	}
	if item := report.Categories[0].Items[0]; !strings.Contains(item.Reason, "nowhere else") {
		t.Fatalf("item = %+v", item)
	}
}

// Stderr in a persisted reason is bounded; stdout that overflows fails the
// call instead of being judged cut off.
func TestCapWriterBoundsGitOutput(t *testing.T) {
	w := &capWriter{max: 4}
	if n, err := w.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatalf("Write = %d, %v; want every byte accepted", n, err)
	}
	if _, _ = w.Write([]byte("gh")); w.buf.String() != "abcd" || !w.over {
		t.Fatalf("buf = %q over = %v", w.buf.String(), w.over)
	}
}

// The deletion push goes wherever git push sends it: a pushurl, several push
// URLs or a pushInsteadOf rewrite can reach another repository that carries
// the same branch at the same tip. Every destination must be the PR's
// repository, or the remote branch is kept everywhere.
func TestBranchRuleDeletesTheRemoteBranchOnlyWhenEveryPushDestinationIsThePRs(t *testing.T) {
	for name, tc := range map[string]struct {
		config  [][]string
		deleted bool
	}{
		"pushurl to a fork":           {config: [][]string{{"remote.origin.pushurl", forkURL}}},
		"PR repo and a fork":          {config: [][]string{{"remote.origin.pushurl", prRemoteURL}, {"--add", "remote.origin.pushurl", forkURL}}},
		"pushInsteadOf to a fork":     {config: [][]string{{"url." + forkURL + ".pushInsteadOf", prRemoteURL}}},
		"insteadOf to a fork":         {config: [][]string{{"url." + forkURL + ".insteadOf", prRemoteURL}}},
		"pushurl to the PR repo, too": {config: [][]string{{"remote.origin.pushurl", prRemoteURL}}, deleted: true},
	} {
		t.Run(name, func(t *testing.T) {
			g := newGitFixture(t)
			head := g.branch("vo/mer-1/root")
			g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
			fork := g.bare("me/fork.git")
			g.git(g.repo, "push", "-q", forkURL, "vo/mer-1/root")
			for _, kv := range tc.config {
				g.git(g.repo, append([]string{"config"}, kv...)...)
			}
			store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 30*24*time.Hour)}}
			src := mergedBranchSource(g, store, "mer-1", head)
			runBranchRules(t, store, RunRequest{}, NewBranchRule(src, openGuard{}, nil))
			if g.git(fork, "branch", "--list", "vo/mer-1/root") == "" {
				t.Fatal("deleted the branch in a repository that is not the PR's")
			}
			if onOrigin := g.git(g.origin, "branch", "--list", "vo/mer-1/root") != ""; onOrigin == tc.deleted {
				t.Fatalf("origin has the branch: %v, want deleted: %v", onOrigin, tc.deleted)
			}
		})
	}
}

// hangingNet makes the network calls made under the guard (the confirm before
// deleting and the remote push) hang until their context ends, and records how
// long each was allowed.
func hangingNet(allowed *[]time.Duration) GitRunner {
	return func(ctx context.Context, repo string, args ...string) (string, error) {
		confirm := len(args) == 3 && args[0] == "ls-remote" && args[1] != "--heads"
		if confirm || (len(args) > 0 && args[0] == "push") {
			deadline, ok := ctx.Deadline()
			if !ok {
				return "", errors.New("no deadline under the guard")
			}
			*allowed = append(*allowed, time.Until(deadline))
			<-ctx.Done()
			return "", ctx.Err()
		}
		return RunGit(ctx, repo, args...)
	}
}

// The project's workspace gate is held while the guard runs, stalling spawn
// and restore: a hung network there costs the short guarded timeout, not a
// full git timeout, and keeps what it could not confirm.
func TestBranchRuleBoundsNetworkCallsUnderTheGuard(t *testing.T) {
	g := newGitFixture(t)
	g.branch("vo/mer-1/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-1/root")
	head := g.branch("vo/mer-2/root")
	g.git(g.repo, "push", "-q", "origin", "vo/mer-2/root")
	store := &fakeStore{policy: `{"branches":{"enabled":true,"deleteRemote":true}}`, sessions: []domain.SessionRecord{worker("mer-1", 60*24*time.Hour), worker("mer-2", 60*24*time.Hour)}}
	src := mergedBranchSource(g, store, "mer-2", head)
	var allowed []time.Duration
	rule := NewBranchRule(src, openGuard{}, hangingNet(&allowed))
	if rule.guardedNet != guardedNetTimeout || guardedNetTimeout >= gitTimeout {
		t.Fatalf("guarded timeout %s, want the short default under %s", rule.guardedNet, gitTimeout)
	}
	rule.guardedNet = 200 * time.Millisecond
	start := time.Now()
	report := runBranchRules(t, store, RunRequest{}, rule)
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("the pass took %s", took)
	}
	if len(allowed) != 2 {
		t.Fatalf("guarded network calls = %d, want the confirm and the push: %+v", len(allowed), report.Categories[0].Items)
	}
	for _, d := range allowed {
		if d > time.Second {
			t.Fatalf("a call under the guard was allowed %s", d)
		}
	}
	// mer-1 relied on the hung confirm: kept. mer-2's merged PR needs no
	// confirm, so its local branch went; its remote push hung: failed, kept.
	if !g.exists("refs/heads/vo/mer-1/root") {
		t.Fatal("deleted a branch the server could not confirm")
	}
	if g.git(g.origin, "branch", "--list", "vo/mer-2/root") == "" {
		t.Fatal("the remote branch went although its push hung")
	}
	if cat := report.Categories[0]; cat.Failed != 1 {
		t.Fatalf("failed = %d, want the hung push reported: %+v", cat.Failed, cat.Items)
	}
}
