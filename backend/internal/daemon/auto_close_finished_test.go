package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type finishedFixture struct {
	store *sqlite.Store
	hooks autoCloseHooks
	rec   domain.SessionRecord
	repo  string
	now   time.Time
}

func finishedGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newFinishedFixture seeds a real store and a real git worktree for a worker
// that finished cleanly, with its done report delivered.
func newFinishedFixture(t *testing.T, note string) *finishedFixture {
	t.Helper()
	ctx := context.Background()
	store := sqlitetest.MustOpen(t)
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "work")
	finishedGit(t, root, "init", "--bare", "-b", "main", origin)
	finishedGit(t, root, "clone", origin, repo)
	finishedGit(t, repo, "symbolic-ref", "HEAD", "refs/heads/main")
	finishedGit(t, repo, "commit", "--allow-empty", "-m", "base")
	finishedGit(t, repo, "push", "origin", "main")
	finishedGit(t, repo, "switch", "-c", "tp/w/root")
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "dpf", Path: repo}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	input := domain.SessionRecord{ProjectID: "dpf", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now.Add(-2 * time.Hour)}}
	input.Metadata.WorkspacePath = repo
	input.Metadata.Branch = "tp/w/root"
	input.Metadata.DiffBaseRef = "main"
	rec, err := store.CreateSession(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	f := &finishedFixture{store: store, rec: rec, repo: repo, now: now}
	f.hooks = autoCloseHooks{store: store, branchWork: (&sessionsvc.Service{}).WorkerBranchWork}
	f.addReport(t, "r1", domain.ReportDone, note, now.Add(-2*time.Hour))
	return f
}

func (f *finishedFixture) addReport(t *testing.T, id string, state domain.ReportState, note string, at time.Time) {
	t.Helper()
	ctx := context.Background()
	report := domain.ReportRecord{ID: id, SessionID: f.rec.ID, ProjectID: "dpf", State: state, Note: note, CreatedAt: at, AvailableAt: at, RepeatCount: 1, DeliveryState: domain.ReportPending}
	if state == domain.ReportDone {
		report.SettlementDeadline = at.Add(domain.ReportSettlementWindow)
	}
	created, err := f.store.CreateReport(ctx, report)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := f.store.ClaimReport(ctx, created.ID, "tok", at); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if _, ok, err := f.store.AcknowledgeReport(ctx, created.ID, "tok", at); err != nil || !ok {
		t.Fatalf("ack: ok=%v err=%v", ok, err)
	}
}

func (f *finishedFixture) keep(t *testing.T) string {
	t.Helper()
	reason, err := f.hooks.FinishedWorkerKeepReason(context.Background(), f.rec, f.now)
	if err != nil {
		t.Fatal(err)
	}
	return reason
}

// B588, end to end over the real store and git: a clean finished worker with
// no PR anywhere is closable.
func TestFinishedWorker_CleanCloses(t *testing.T) {
	f := newFinishedFixture(t, "Research written up in the artifact.")
	if got := f.keep(t); got != "" {
		t.Fatalf("kept: %s", got)
	}
}

// B588, dpf-4: Bitbucket PR, empty prs, the note is the only record of it.
func TestFinishedWorker_BitbucketPRWithEmptyPRsIsKept(t *testing.T) {
	f := newFinishedFixture(t, "Opened https://bitbucket.org/acme/dpf/pull-requests/2 for the fix.")
	prs, err := f.store.ListPRsBySession(context.Background(), f.rec.ID)
	if err != nil || len(prs) != 0 {
		t.Fatalf("precondition: prs=%v err=%v", prs, err)
	}
	if got := f.keep(t); !strings.Contains(got, "pull-requests/2") {
		t.Fatalf("reason = %q, want the Bitbucket PR named", got)
	}
}

// B588: same empty prs, no PR named at all: the pushed branch alone keeps it.
func TestFinishedWorker_BitbucketPushedBranchIsKept(t *testing.T) {
	f := newFinishedFixture(t, "Pushed the branch for the owner.")
	finishedGit(t, f.repo, "commit", "--allow-empty", "-m", "work")
	finishedGit(t, f.repo, "push", "origin", "tp/w/root")
	if got := f.keep(t); !strings.Contains(got, "unmerged commits") {
		t.Fatalf("reason = %q, want unmerged commits", got)
	}
}

func TestFinishedWorker_UncommittedChangesKept(t *testing.T) {
	f := newFinishedFixture(t, "Finished.")
	if err := os.WriteFile(filepath.Join(f.repo, "notes.txt"), []byte("wip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := f.keep(t); !strings.Contains(got, "uncommitted") {
		t.Fatalf("reason = %q, want uncommitted", got)
	}
}

// An untracked node_modules symlink is not work (the worktree teardown's rule).
func TestFinishedWorker_NodeModulesSymlinkIsNotWork(t *testing.T) {
	f := newFinishedFixture(t, "Finished.")
	if err := os.Symlink(t.TempDir(), filepath.Join(f.repo, "node_modules")); err != nil {
		t.Fatal(err)
	}
	if got := f.keep(t); got != "" {
		t.Fatalf("kept: %s", got)
	}
}

func TestFinishedWorker_LastReportNeedsInputKept(t *testing.T) {
	f := newFinishedFixture(t, "Finished.")
	f.addReport(t, "r2", domain.ReportNeedsInput, "Which option?", f.now.Add(-time.Hour))
	if got := f.keep(t); !strings.Contains(got, "needs_input") {
		t.Fatalf("reason = %q", got)
	}
}

func TestFinishedWorker_RunningReviewKept(t *testing.T) {
	f := newFinishedFixture(t, "Finished.")
	ctx := context.Background()
	review := domain.Review{ID: "rev-1", SessionID: f.rec.ID, ProjectID: f.rec.ProjectID, Harness: domain.ReviewerClaudeCode, PRURL: "https://github.com/o/r/pull/1", CreatedAt: f.now, UpdatedAt: f.now}
	if err := f.store.UpsertReview(ctx, review); err != nil {
		t.Fatal(err)
	}
	run := domain.ReviewRun{ID: "run-1", ReviewID: review.ID, SessionID: f.rec.ID, Harness: review.Harness, TriggerSource: domain.ReviewTriggerManual, PRURL: review.PRURL, TargetSHA: "sha", Status: domain.ReviewRunRunning, Body: "b", CreatedAt: f.now}
	if err := f.store.InsertReviewRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	if got := f.keep(t); !strings.Contains(got, "review is running") {
		t.Fatalf("reason = %q", got)
	}
}

func TestFinishedWorker_ProjectSettingOffKept(t *testing.T) {
	f := newFinishedFixture(t, "Finished.")
	off := false
	if err := f.store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "dpf", Path: f.repo, Config: domain.ProjectConfig{TerminateOnPRMerge: &off}}); err != nil {
		t.Fatal(err)
	}
	if got := f.keep(t); !strings.Contains(got, "project auto-close is off") {
		t.Fatalf("reason = %q", got)
	}
}
