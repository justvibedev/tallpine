package cleanup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// fakeRemover behaves like the session manager: it lists a session's
// worktrees, and before removing runs the caller's check on the current record
// (standing in for the re-read under the guard).
type fakeRemover struct {
	mu      sync.Mutex
	calls   []domain.SessionID
	removed []domain.SessionID
	outcome WorktreeOutcome
	err     error
	// remove deletes the path like the real session manager does.
	remove  func(id domain.SessionID)
	paths   func(id domain.SessionID) []string
	current func(id domain.SessionID) domain.SessionRecord
	// beforeCheck runs between the plan and the check under the guard.
	beforeCheck func(id domain.SessionID)
}

func (f *fakeRemover) WorktreePaths(_ context.Context, id domain.SessionID) ([]string, error) {
	return f.paths(id), nil
}

func (f *fakeRemover) CleanupSession(ctx context.Context, id domain.SessionID, check WorktreeCheck) (WorktreeOutcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, id)
	f.mu.Unlock()
	if f.err != nil || f.outcome != (WorktreeOutcome{}) {
		return f.outcome, f.err
	}
	if f.beforeCheck != nil {
		f.beforeCheck(id)
	}
	if check != nil {
		if reason := check(ctx, f.current(id), f.paths(id)); reason != "" {
			return WorktreeOutcome{Kept: reason}, nil
		}
	}
	if f.remove != nil {
		f.remove(id)
	}
	f.mu.Lock()
	f.removed = append(f.removed, id)
	f.mu.Unlock()
	return WorktreeOutcome{Removed: true}, nil
}

func (f *fakeRemover) called() []domain.SessionID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]domain.SessionID(nil), f.calls...)
}

type worktreeFixture struct {
	t        *testing.T
	root     string
	now      time.Time
	keep     map[string]string
	inspErr  map[string]error
	merged   []bool
	store    *fakeStore
	remover  *fakeRemover
	paths    map[domain.SessionID]string
	children map[domain.SessionID][]string
}

func newWorktreeFixture(t *testing.T) *worktreeFixture {
	f := &worktreeFixture{
		t: t, root: filepath.Join(t.TempDir(), "worktrees"),
		now:  time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
		keep: map[string]string{}, inspErr: map[string]error{},
		store: &fakeStore{policy: "{}"}, paths: map[domain.SessionID]string{}, children: map[domain.SessionID][]string{},
	}
	f.remover = &fakeRemover{
		remove: func(id domain.SessionID) { _ = os.RemoveAll(f.paths[id]) },
		paths: func(id domain.SessionID) []string {
			return append([]string{f.paths[id]}, f.children[id]...)
		},
		current: func(id domain.SessionID) domain.SessionRecord {
			for _, rec := range f.store.sessions {
				if rec.ID == id {
					return rec
				}
			}
			return domain.SessionRecord{}
		},
	}
	return f
}

// session adds a session whose worktree holds one 1 KiB file.
func (f *worktreeFixture) session(id domain.SessionID, terminated bool, endedAgo time.Duration, path string) {
	f.t.Helper()
	if path == "" {
		path = filepath.Join(f.root, "mer", string(id))
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "main.go"), make([]byte, 1024), 0o600); err != nil {
		f.t.Fatal(err)
	}
	f.paths[id] = path
	rec := domain.SessionRecord{ID: id, ProjectID: "mer", IsTerminated: terminated, UpdatedAt: f.now.Add(-endedAgo)}
	rec.Metadata.WorkspacePath = path
	f.store.sessions = append(f.store.sessions, rec)
}

func (f *worktreeFixture) run(req RunRequest) (Report, *WorktreeRule) {
	f.t.Helper()
	rule := NewWorktreeRule(f.root, f.remover, func(_ context.Context, path string, requireMerged bool) (string, error) {
		f.merged = append(f.merged, requireMerged)
		return f.keep[path], f.inspErr[path]
	})
	return f.runWith(rule, req), rule
}

func (f *worktreeFixture) runWith(rule *WorktreeRule, req RunRequest) Report {
	f.t.Helper()
	svc := New(f.store, []Rule{rule}, Config{Pause: -1, StartDelay: -1, Clock: func() time.Time { return f.now }})
	report, err := svc.Run(context.Background(), req)
	if err != nil {
		f.t.Fatal(err)
	}
	return report
}

func itemFor(t *testing.T, report Report, id domain.SessionID) Item {
	t.Helper()
	for _, cat := range report.Categories {
		for _, item := range cat.Items {
			if item.SessionID == id {
				return item
			}
		}
	}
	t.Fatalf("no item for %s in %+v", id, report.Categories)
	return Item{}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestWorktreeRuleNeverTouchesLiveOrDirtyWork(t *testing.T) {
	f := newWorktreeFixture(t)
	day := 24 * time.Hour
	f.session("live", false, 30*day, "")
	f.session("dirty", true, 30*day, "")
	f.keep[f.paths["dirty"]] = "uncommitted changes"
	f.session("unpushed", true, 30*day, "")
	f.keep[f.paths["unpushed"]] = "commits that are not merged or pushed"
	f.session("unknown", true, 30*day, "")
	f.inspErr[f.paths["unknown"]] = errors.New("not a git repository")
	f.session("recent", true, time.Hour, "")
	f.session("outside", true, 30*day, filepath.Join(t.TempDir(), "checkout"))
	// A terminated predecessor sharing its worktree with a live successor.
	f.session("successor", false, 0, filepath.Join(f.root, "mer", "shared"))
	f.session("predecessor", true, 30*day, filepath.Join(f.root, "mer", "shared"))

	report, _ := f.run(RunRequest{})
	if calls := f.remover.called(); len(calls) != 0 {
		t.Fatalf("remover called for %v, want no removal at all", calls)
	}
	for id, reason := range map[domain.SessionID]string{
		"dirty": "uncommitted changes", "unpushed": "not merged or pushed", "unknown": "could not be read", "recent": "ended less than",
		"outside": "outside the managed", "predecessor": "in use by a live session",
	} {
		item := itemFor(t, report, id)
		if item.Status != StatusKept || !strings.Contains(item.Reason, reason) {
			t.Errorf("%s: %+v, want kept (%s)", id, item, reason)
		}
	}
	for id, path := range f.paths {
		if !exists(path) {
			t.Errorf("%s worktree removed", id)
		}
	}
	for _, cat := range report.Categories {
		for _, item := range cat.Items {
			if item.SessionID == "live" || item.SessionID == "successor" {
				t.Errorf("live session reported as a candidate: %+v", item)
			}
		}
	}
}

func TestWorktreeRuleDryRunRemovesNothing(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("done", true, 10*24*time.Hour, "")
	report, _ := f.run(RunRequest{DryRun: true})
	item := itemFor(t, report, "done")
	if item.Status != StatusWouldFree || item.Bytes != 1024 || !strings.Contains(item.Reason, "gitignored files") {
		t.Fatalf("dry run item = %+v, want would_free 1024 bytes with the gitignored-files note", item)
	}
	if len(f.remover.called()) != 0 || !exists(f.paths["done"]) {
		t.Fatal("a dry run removed a worktree")
	}
}

func TestWorktreeRuleRemovesCleanWorktreesPastRetention(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("done", true, 10*24*time.Hour, "")
	report, _ := f.run(RunRequest{})
	item := itemFor(t, report, "done")
	if item.Status != StatusFreed || item.Bytes != 1024 || report.FreedBytes != 1024 || !strings.Contains(item.Reason, ".env") {
		t.Fatalf("item = %+v freed=%d, want 1024 bytes freed", item, report.FreedBytes)
	}
	if exists(f.paths["done"]) {
		t.Fatal("worktree still present")
	}
	if len(f.merged) == 0 || !f.merged[0] {
		t.Fatalf("inspector got requireMerged=%v, want the default policy's true", f.merged)
	}
	if got := f.remover.called(); len(got) != 1 || got[0] != "done" {
		t.Fatalf("remover calls = %v", got)
	}
}

func TestWorktreeRuleBacksOffAfterARefusal(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("done", true, 10*24*time.Hour, "")
	f.remover.outcome = WorktreeOutcome{Kept: "shell terminal still open"}
	report, rule := f.run(RunRequest{Pass: PassLight})
	if item := itemFor(t, report, "done"); item.Status != StatusKept || item.Reason != "shell terminal still open" {
		t.Fatalf("refused item = %+v", item)
	}
	f.runWith(rule, RunRequest{Pass: PassLight})
	if n := len(f.remover.called()); n != 1 {
		t.Fatalf("light pass retried a refused worktree (%d calls)", n)
	}
	f.runWith(rule, RunRequest{Pass: PassFull})
	if n := len(f.remover.called()); n != 2 {
		t.Fatalf("full pass did not retry the refused worktree (%d calls)", n)
	}
}

// inspectRepo is a repository with a bare origin that main is pushed to.
type inspectRepo struct {
	t    *testing.T
	repo string
}

func (g *inspectRepo) git(dir string, args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newInspectRepo(t *testing.T) *inspectRepo {
	t.Helper()
	g := &inspectRepo{t: t, repo: filepath.Join(t.TempDir(), "repo")}
	origin := filepath.Join(t.TempDir(), "origin.git")
	g.git(filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	g.git(filepath.Dir(g.repo), "init", "-q", "-b", "main", g.repo)
	g.git(g.repo, "commit", "-q", "--allow-empty", "-m", "init")
	g.git(g.repo, "remote", "add", "origin", origin)
	g.git(g.repo, "push", "-q", "origin", "main")
	return g
}

func TestGitInspect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	g := newInspectRepo(t)
	ctx := context.Background()
	repo := g.repo
	inspect := func(requireMerged bool) string {
		t.Helper()
		reason, err := GitInspect(ctx, repo, requireMerged)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	// On main, pushed: nothing to keep.
	if r := inspect(true); r != "" {
		t.Fatalf("clean pushed main kept: %q", r)
	}
	// A branch with a commit only on itself.
	g.git(repo, "checkout", "-q", "-b", "vo/mer-1/root")
	g.git(repo, "commit", "-q", "--allow-empty", "-m", "work")
	if r := inspect(true); !strings.Contains(r, "not merged or pushed") {
		t.Fatalf("unpushed commit with requireMerged: %q", r)
	}
	if r := inspect(false); r != "" {
		t.Fatalf("unpushed commit without requireMerged kept: %q (its branch survives)", r)
	}
	g.git(repo, "push", "-q", "origin", "vo/mer-1/root")
	if r := inspect(true); r != "" {
		t.Fatalf("pushed branch kept: %q", r)
	}
	// Untracked file.
	if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := inspect(true); r != "uncommitted changes" {
		t.Fatalf("untracked file: %q", r)
	}
	// Stash on the branch.
	g.git(repo, "stash", "push", "-q", "-u", "-m", "wip")
	if r := inspect(true); !strings.Contains(r, "stash") {
		t.Fatalf("stash on the branch: %q", r)
	}
	g.git(repo, "stash", "drop", "-q")
	// A rebase left in progress.
	gitDir := g.git(repo, "rev-parse", "--absolute-git-dir")
	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if r := inspect(false); !strings.Contains(r, "in progress") {
		t.Fatalf("rebase in progress: %q", r)
	}
	if err := os.RemoveAll(filepath.Join(gitDir, "rebase-merge")); err != nil {
		t.Fatal(err)
	}
	// A commit on a detached HEAD that no ref contains, even without requireMerged.
	g.git(repo, "checkout", "-q", "--detach")
	g.git(repo, "commit", "-q", "--allow-empty", "-m", "detached")
	if r := inspect(false); !strings.Contains(r, "detached") {
		t.Fatalf("detached commit: %q", r)
	}
	if _, err := GitInspect(ctx, t.TempDir(), true); err == nil {
		t.Fatal("a non-repository read as safe")
	}
}

func TestWorktreeRuleRechecksUnderTheGuardBeforeTeardown(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("done", true, 10*24*time.Hour, "")
	f.session("restored", true, 10*24*time.Hour, "")
	// Between the plan and the teardown: "done" gets a rebase left in
	// progress; "restored" is restored, worked on and terminated again.
	f.remover.beforeCheck = func(id domain.SessionID) {
		switch id {
		case "done":
			f.keep[f.paths["done"]] = "a rebase, merge or similar operation is in progress"
		case "restored":
			for i := range f.store.sessions {
				if f.store.sessions[i].ID == "restored" {
					f.store.sessions[i].UpdatedAt = f.now
				}
			}
		}
	}
	report, _ := f.run(RunRequest{})
	if f.remover.removed != nil {
		t.Fatalf("removed %v although their state changed after the plan", f.remover.removed)
	}
	if item := itemFor(t, report, "done"); item.Status != StatusKept || !strings.Contains(item.Reason, "in progress") {
		t.Fatalf("done = %+v, want kept by the re-inspection", item)
	}
	if item := itemFor(t, report, "restored"); item.Status != StatusKept || !strings.Contains(item.Reason, "ended less than") {
		t.Fatalf("restored = %+v, want kept by the re-checked retention", item)
	}
	for _, id := range []domain.SessionID{"done", "restored"} {
		if !exists(f.paths[id]) {
			t.Fatalf("%s worktree removed", id)
		}
	}
}

func TestWorktreeRuleInspectsEveryRepoOfAWorkspaceSession(t *testing.T) {
	f := newWorktreeFixture(t)
	f.session("multi", true, 10*24*time.Hour, "")
	child := filepath.Join(f.paths["multi"], "api")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	f.children["multi"] = []string{child}
	f.keep[child] = "commits on a detached HEAD that no branch contains"
	report, _ := f.run(RunRequest{})
	if len(f.remover.called()) != 0 || !exists(f.paths["multi"]) {
		t.Fatal("a workspace session was removed although a child repo must be kept")
	}
	if item := itemFor(t, report, "multi"); item.Status != StatusKept || !strings.Contains(item.Reason, "detached") || !strings.Contains(item.Reason, "in api") {
		t.Fatalf("item = %+v, want kept naming the child repo", item)
	}
	delete(f.keep, child)
	f.inspErr[child] = errors.New("boom")
	if report, _ := f.run(RunRequest{}); itemFor(t, report, "multi").Status != StatusKept {
		t.Fatal("an unreadable child repo did not keep the session")
	}
}

// A real meta-repo: the root ignores its child repo, so the root reads clean
// while the child is mid-rebase or holds detached commits.
func TestGitInspectAllKeepsAnIgnoredChildRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := newInspectRepo(t)
	if err := os.WriteFile(filepath.Join(root.repo, ".gitignore"), []byte("api/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root.git(root.repo, "add", ".gitignore")
	root.git(root.repo, "commit", "-q", "-m", "ignore children")
	root.git(root.repo, "push", "-q", "origin", "main")
	child := filepath.Join(root.repo, "api")
	root.git(root.repo, "init", "-q", "-b", "main", child)
	root.git(child, "commit", "-q", "--allow-empty", "-m", "child init")
	root.git(child, "update-ref", "refs/remotes/origin/main", "HEAD")
	rule := NewWorktreeRule(filepath.Dir(root.repo), nil, nil)
	ctx := context.Background()
	paths := []string{root.repo, child}
	if reason := rule.inspectAll(ctx, paths, true, nil); reason != "" {
		t.Fatalf("clean meta-repo kept: %q", reason)
	}
	gitDir := root.git(child, "rev-parse", "--absolute-git-dir")
	if err := os.MkdirAll(filepath.Join(gitDir, "rebase-merge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if reason := rule.inspectAll(ctx, paths, true, nil); !strings.Contains(reason, "in progress") || !strings.Contains(reason, "in api") {
		t.Fatalf("child mid-rebase: %q", reason)
	}
	if err := os.RemoveAll(filepath.Join(gitDir, "rebase-merge")); err != nil {
		t.Fatal(err)
	}
	root.git(child, "checkout", "-q", "--detach")
	root.git(child, "commit", "-q", "--allow-empty", "-m", "detached work")
	if reason := rule.inspectAll(ctx, paths, false, nil); !strings.Contains(reason, "detached") {
		t.Fatalf("child with a detached commit: %q", reason)
	}
}

// A directory that is not itself a repository must not be judged by the
// repository above it.
func TestGitInspectRefusesAParentRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	g := newInspectRepo(t)
	sub := filepath.Join(g.repo, "plain-dir")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := GitInspect(context.Background(), sub, true); err == nil {
		t.Fatal("a directory without its own .git was inspected through the parent repo")
	}
}

func TestEscapeGlob(t *testing.T) {
	if got := escapeGlob("vo/a*b?[c]"); got != `vo/a\*b\?\[c\]` {
		t.Fatalf("escapeGlob = %q", got)
	}
}

func TestSafetyCheck(t *testing.T) {
	root := t.TempDir()
	in := filepath.Join(root, "mer", "mer-1")
	if err := os.MkdirAll(filepath.Join(in, "api"), 0o700); err != nil {
		t.Fatal(err)
	}
	var merged []bool
	keep := map[string]string{}
	check := SafetyCheck(root, func(_ context.Context, path string, requireMerged bool) (string, error) {
		merged = append(merged, requireMerged)
		return keep[path], nil
	})
	ctx := context.Background()
	ended := domain.SessionRecord{ID: "mer-1", IsTerminated: true}
	paths := []string{in, filepath.Join(in, "api")}
	if reason := check(ctx, ended, paths); reason != "" {
		t.Fatalf("safe session kept: %q", reason)
	}
	for _, rm := range merged {
		if rm {
			t.Fatal("manual cleanup applied requireMerged")
		}
	}
	keep[filepath.Join(in, "api")] = "stashed changes on its branch"
	if reason := check(ctx, ended, paths); !strings.Contains(reason, "stash") {
		t.Fatalf("child with a stash: %q", reason)
	}
	if reason := check(ctx, domain.SessionRecord{ID: "mer-1"}, paths); !strings.Contains(reason, "live") {
		t.Fatalf("live session: %q", reason)
	}
	if reason := check(ctx, ended, []string{t.TempDir()}); !strings.Contains(reason, "outside") {
		t.Fatalf("outside the root: %q", reason)
	}
}
