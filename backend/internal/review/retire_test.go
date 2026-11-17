package review

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// retireFixture seeds one terminal reviewer pane last touched at base, with an
// optional running pass, and an engine whose clock is base+age.
func retireFixture(t *testing.T, age time.Duration, withRunning bool) (*Engine, *fakeLauncher, domain.SessionRecord) {
	t.Helper()
	status, verdict := domain.ReviewRunComplete, domain.VerdictChangesRequested
	if withRunning {
		status, verdict = domain.ReviewRunRunning, domain.VerdictNone
	}
	return retireFixtureWithRun(t, age, status, verdict)
}

// retireFixtureWithRun is retireFixture with the pass's status and verdict.
func retireFixtureWithRun(t *testing.T, age time.Duration, status domain.ReviewRunStatus, verdict domain.ReviewVerdict) (*Engine, *fakeLauncher, domain.SessionRecord) {
	t.Helper()
	ctx := context.Background()
	st := newSQLiteReviewStore(t)
	worker := liveWorker()
	seedReviewWorker(t, st, worker)
	base := time.Now().UTC().Truncate(time.Second).Add(-24 * time.Hour)
	if err := st.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerClaudeCode,
		PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-" + string(worker.ID),
		ReviewerActivityState: domain.ActivityIdle, CreatedAt: base, UpdatedAt: base,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	if err := st.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-1", ReviewID: "rev-1", SessionID: worker.ID, Harness: domain.ReviewerClaudeCode,
		PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: status, Verdict: verdict, CreatedAt: base,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	launcher := &fakeLauncher{}
	eng := New(Deps{
		Store: st, Sessions: fakeSessions{rec: worker, ok: true}, PRs: prAt("sha1"), Projects: fakeProjects{}, Launcher: launcher,
		Clock: func() time.Time { return base.Add(age) },
	})
	return eng, launcher, worker
}

func handleOf(t *testing.T, eng *Engine, worker domain.SessionRecord) string {
	t.Helper()
	got, ok, err := eng.store.GetReviewBySession(context.Background(), worker.ID)
	if err != nil || !ok {
		t.Fatalf("get review: ok=%v err=%v", ok, err)
	}
	return got.ReviewerHandleID
}

// B493: a pane left after a completed pass is destroyed once it has been idle
// past the retire grace, and its handle is cleared so the next trigger starts fresh.
func TestRetireIdleReviewersReapsPaneAfterCompletedPass(t *testing.T) {
	eng, launcher, worker := retireFixture(t, 2*reviewerRetireGrace, false)
	n, err := eng.RetireIdleReviewers(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 1, nil", n, err)
	}
	if launcher.destroyedHandle != "review-"+string(worker.ID) {
		t.Fatalf("destroyed handle = %q", launcher.destroyedHandle)
	}
	if h := handleOf(t, eng, worker); h != "" {
		t.Fatalf("handle after retire = %q, want cleared", h)
	}
}

func TestRetireIdleReviewersKeepsPaneWithinGrace(t *testing.T) {
	eng, launcher, worker := retireFixture(t, time.Minute, false)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 0 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 0, nil", n, err)
	}
	if launcher.destroyed || handleOf(t, eng, worker) == "" {
		t.Fatal("a pane inside the retire grace must be kept for reuse")
	}
}

func TestRetireIdleReviewersKeepsPaneWithRunningPass(t *testing.T) {
	eng, launcher, worker := retireFixture(t, 2*reviewerRetireGrace, true)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 0 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 0, nil", n, err)
	}
	if launcher.destroyed || handleOf(t, eng, worker) == "" {
		t.Fatal("a pane with a running pass must never be retired")
	}
}

// A stale row of one harness shares the worker's pane handle with a fresher
// row of another: the pane must not be destroyed under the live reviewer.
func TestRetireIdleReviewersKeepsPaneSharedWithFresherRow(t *testing.T) {
	eng, launcher, worker := retireFixture(t, 2*reviewerRetireGrace, false)
	ctx := context.Background()
	now := eng.clock()
	if err := eng.store.UpsertReview(ctx, domain.Review{
		ID: "rev-2", SessionID: worker.ID, ProjectID: worker.ProjectID, Harness: domain.ReviewerCodex,
		PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-" + string(worker.ID),
		ReviewerActivityState: domain.ActivityActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert fresh row: %v", err)
	}
	if _, err := eng.RetireIdleReviewers(ctx); err != nil {
		t.Fatalf("RetireIdleReviewers: %v", err)
	}
	if launcher.destroyed {
		t.Fatal("destroyed a pane still held by a fresher harness row")
	}
	got, ok, err := eng.store.GetReviewBySessionAndHarness(ctx, worker.ID, domain.ReviewerCodex)
	if err != nil || !ok || got.ReviewerHandleID == "" {
		t.Fatalf("fresh row handle = %q ok=%v err=%v, want kept", got.ReviewerHandleID, ok, err)
	}
}

// B754: an approved pass leaves nothing for the reviewer to re-check, so its
// pane is retired after the short approved grace instead of the full one.
func TestRetireIdleReviewersReapsApprovedPaneAfterShortGrace(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunComplete, domain.VerdictApproved)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 1 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 1, nil", n, err)
	}
	if !launcher.destroyed || handleOf(t, eng, worker) != "" {
		t.Fatal("an approved pane idle past the approved grace must be destroyed and its handle cleared")
	}
}

// The approval stays approved once delivered to the worker.
func TestRetireIdleReviewersReapsDeliveredApprovedPane(t *testing.T) {
	eng, launcher, _ := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunDelivered, domain.VerdictApproved)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 1 || !launcher.destroyed {
		t.Fatalf("RetireIdleReviewers = %d, %v, destroyed=%v; want 1, nil, true", n, err, launcher.destroyed)
	}
}

func TestRetireIdleReviewersKeepsApprovedPaneWithinShortGrace(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, approvedRetireGrace/2, domain.ReviewRunComplete, domain.VerdictApproved)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 0 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 0, nil", n, err)
	}
	if launcher.destroyed || handleOf(t, eng, worker) == "" {
		t.Fatal("an approved pane inside the approved grace must be kept")
	}
}

// A changes-requested pass keeps the pane for the full grace: the worker's
// fix is about to be re-reviewed.
func TestRetireIdleReviewersKeepsChangesRequestedPanePastShortGrace(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunComplete, domain.VerdictChangesRequested)
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 0 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 0, nil", n, err)
	}
	if launcher.destroyed || handleOf(t, eng, worker) == "" {
		t.Fatal("a changes-requested pane must be kept for the full retire grace")
	}
}

// insertRetireRun adds a completed pass on rev-1 for prURL, offset from the
// fixture's base timestamp.
func insertRetireRun(t *testing.T, eng *Engine, worker domain.SessionRecord, id, prURL string, verdict domain.ReviewVerdict, at time.Time) {
	t.Helper()
	if err := eng.store.InsertReviewRun(context.Background(), domain.ReviewRun{
		ID: id, ReviewID: "rev-1", SessionID: worker.ID, Harness: domain.ReviewerClaudeCode,
		PRURL: prURL, TargetSHA: "sha1", Status: domain.ReviewRunComplete, Verdict: verdict, CreatedAt: at,
	}); err != nil {
		t.Fatalf("insert run %s: %v", id, err)
	}
}

func assertRetireKeeps(t *testing.T, eng *Engine, launcher *fakeLauncher, worker domain.SessionRecord) {
	t.Helper()
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 0 {
		t.Fatalf("RetireIdleReviewers = %d, %v; want 0, nil", n, err)
	}
	if launcher.destroyed || handleOf(t, eng, worker) == "" {
		t.Fatal("a pane with a PR still awaiting a fix must be kept for the full retire grace")
	}
}

// A batch inserts one run per PR with the same timestamp: one approved PR must
// not shorten the grace while another PR's changes are still requested.
func TestRetireIdleReviewersKeepsMixedVerdictBatchPastShortGrace(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunComplete, domain.VerdictApproved)
	base := eng.clock().Add(-2 * approvedRetireGrace)
	insertRetireRun(t, eng, worker, "run-2", "https://github.com/o/r/pull/2", domain.VerdictChangesRequested, base)
	assertRetireKeeps(t, eng, launcher, worker)
}

// A later approval of one PR does not settle an earlier changes-requested PR.
func TestRetireIdleReviewersKeepsEarlierChangesRequestedPRPastShortGrace(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunComplete, domain.VerdictChangesRequested)
	base := eng.clock().Add(-2 * approvedRetireGrace)
	insertRetireRun(t, eng, worker, "run-2", "https://github.com/o/r/pull/2", domain.VerdictApproved, base.Add(time.Second))
	assertRetireKeeps(t, eng, launcher, worker)
}

// A PR re-reviewed to approval no longer holds the pane: only each PR's latest
// verdict counts.
func TestRetireIdleReviewersReapsPaneOnceEveryPRApproved(t *testing.T) {
	eng, launcher, worker := retireFixtureWithRun(t, 2*approvedRetireGrace, domain.ReviewRunComplete, domain.VerdictChangesRequested)
	base := eng.clock().Add(-2 * approvedRetireGrace)
	insertRetireRun(t, eng, worker, "run-2", "https://github.com/o/r/pull/1", domain.VerdictApproved, base.Add(time.Second))
	if n, err := eng.RetireIdleReviewers(context.Background()); err != nil || n != 1 || !launcher.destroyed {
		t.Fatalf("RetireIdleReviewers = %d, %v, destroyed=%v; want 1, nil, true", n, err, launcher.destroyed)
	}
}
