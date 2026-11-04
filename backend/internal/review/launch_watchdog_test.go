package review

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type screenLauncher struct {
	*fakeLauncher
	screen string
	err    error
}

func (l screenLauncher) Screen(context.Context, string, int) (string, error) { return l.screen, l.err }

func watchdogFixture(reviewUpdated time.Time) (*fakeStore, *fakeLauncher, *Engine, screenLauncher) {
	now := time.Unix(0, 0).UTC()
	created := now.Add(-reviewerLaunchGrace - time.Minute)
	if reviewUpdated.IsZero() {
		reviewUpdated = created
	}
	store := &fakeStore{
		review: &domain.Review{
			ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
			ReviewerActivityState: domain.ActivityActive, InterfaceMode: domain.ReviewerInterfaceTUI,
			UpdatedAt: reviewUpdated,
		},
		runs: []domain.ReviewRun{{
			ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
			PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
			CreatedAt: created,
		}},
	}
	fake := &fakeLauncher{alive: true}
	launcher := screenLauncher{fakeLauncher: fake, screen: "\n  Choose a model\n\n  1. Default (recommended)\n\n"}
	return store, fake, newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher), launcher
}

// A Claude terminal that never reported a hook (stuck on a startup screen) fails
// its pass with the screen as the error and releases the wedged pane.
func TestListFailsRunOfLiveReviewerThatNeverStarted(t *testing.T) {
	store, fake, eng, _ := watchdogFixture(time.Time{})
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunFailed ||
		!strings.HasPrefix(got.Runs[0].Body, ReviewerNotStartedPrefix) || !strings.Contains(got.Runs[0].Body, "Choose a model") {
		t.Fatalf("runs = %+v, want a failed never-started pass carrying the screen", got.Runs)
	}
	if !IsUnsubmittedReviewerFailure(got.Runs[0].Body) {
		t.Fatalf("a never-started pass must be eligible for the one retry")
	}
	if !fake.destroyed || store.review.ReviewerHandleID != "" {
		t.Fatalf("destroyed=%v handle=%q, want the wedged pane released", fake.destroyed, store.review.ReviewerHandleID)
	}
}

// A reviewer whose hook fired after the pass began is working, however long.
func TestListKeepsRunOfLiveReviewerThatReportedActivity(t *testing.T) {
	hook := time.Unix(0, 0).UTC().Add(-reviewerLaunchGrace)
	_, _, eng, _ := watchdogFixture(hook)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("runs = %+v, want the pass still running", got.Runs)
	}
}

// The reviewer's first hook lands while Spawn is still in flight, before the
// trigger's closing upsert. That upsert must not erase it, or a healthy reviewer
// that is still thinking at the grace is failed and its pane destroyed.
func TestTriggerKeepsHookDeliveredDuringSpawnPastLaunchGrace(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{interfaceMode: domain.ReviewerInterfaceTUI, alive: true, handle: "review-mer-1"}
	start := time.Unix(0, 0).UTC()
	launcher.onSpawn = func(LaunchSpec) {
		// The row exists (the trigger recorded it) and the hook stamps a later second.
		store.review.UpdatedAt = start.Add(20 * time.Second)
		row := store.reviews[domain.ReviewerClaudeCode]
		row.UpdatedAt = store.review.UpdatedAt
		store.reviews[domain.ReviewerClaudeCode] = row
	}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: launcher, screen: "thinking"})
	if _, err := triggerWithSource(context.Background(), eng, "mer-1", domain.ReviewerClaudeCode, domain.AgentConfig{}, domain.ReviewTriggerManual); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	eng.clock = func() time.Time { return start.Add(reviewerLaunchGrace + 30*time.Second) }
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("runs = %+v, want the working reviewer's pass kept", got.Runs)
	}
	if launcher.destroyed {
		t.Fatal("a working reviewer's pane was destroyed")
	}
}

func watchdogHarnessFixture(harness domain.ReviewerHarness, mode domain.ReviewerInterfaceMode, batchID string) (*fakeStore, *fakeLauncher, *Engine) {
	store, fake, _, _ := watchdogFixture(time.Time{})
	store.review.Harness, store.review.InterfaceMode = harness, mode
	store.runs[0].Harness, store.runs[0].BatchID = harness, batchID
	return store, fake, nil
}

// A Chat reviewer whose prompt the provider never accepted fails with that reason.
func TestListFailsChatReviewerWhosePromptNeverReachedProvider(t *testing.T) {
	store, fake, _ := watchdogHarnessFixture(domain.ReviewerCodex, domain.ReviewerInterfaceChat, "batch-1")
	store.review.ReviewerHandleID = "review-chat:rev-1"
	store.undispatchedBatches = map[string]bool{"batch-1": true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, fake)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunFailed || !strings.HasPrefix(got.Runs[0].Body, ReviewerNotStartedPrefix) {
		t.Fatalf("runs = %+v, want a failed never-started chat pass", got.Runs)
	}
	if !fake.destroyed {
		t.Fatal("the wedged chat controller was not stopped")
	}
}

func TestListKeepsChatReviewerWhosePromptReachedProvider(t *testing.T) {
	store, fake, _ := watchdogHarnessFixture(domain.ReviewerCodex, domain.ReviewerInterfaceChat, "batch-1")
	store.review.ReviewerHandleID = "review-chat:rev-1"
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, fake)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("runs = %+v, want the dispatched pass kept", got.Runs)
	}
}

// A terminal reviewer without activity hooks is judged by its pane: blank fails, output keeps.
func TestListHooklessTerminalReviewerFailsOnlyOnBlankPane(t *testing.T) {
	for _, tc := range []struct {
		name, screen string
		wantFail     bool
		err          error
	}{{"blank pane", "\n   \n", true, nil}, {"shows output", "kiro> reviewing\n", false, nil}, {"unreadable pane", "", false, errors.New("pty-host unavailable")}} {
		t.Run(tc.name, func(t *testing.T) {
			store, fake, _ := watchdogHarnessFixture(domain.ReviewerKiro, domain.ReviewerInterfaceTUI, "")
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: fake, screen: tc.screen, err: tc.err})
			got, err := eng.List(context.Background(), "mer-1")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			failed := len(got.Runs) == 1 && got.Runs[0].Status == domain.ReviewRunFailed
			if failed != tc.wantFail {
				t.Fatalf("runs = %+v, wantFail=%v", got.Runs, tc.wantFail)
			}
		})
	}
}

func TestListIdleTerminalReviewerOnUsageLimitWaitsForReset(t *testing.T) {
	now := time.Unix(0, 0).UTC()
	limit := "You've hit your limit · resets 3pm (UTC)"
	for _, tc := range []struct {
		name, screen, want string
	}{
		{"limit", "some output\n⎿  " + limit + "\n> ", ReviewerUsageLimitPrefix + limit},
		// Run 99207668 (B348): Claude injected the grace notice, the reviewer stopped
		// without submitting, and the pane showed Claude's own limit line.
		{"grace notice then stop", "[Usage limit reached; a short grace allowance remains, then this turn is cut off without warning.]\nYour usage limit was reached, so I stopped before posting a review.\nUsage limit reached · continuing automatically at 5:20pm · esc to cancel\n> ",
			ReviewerUsageLimitPrefix + "Usage limit reached"},
		{"plain idle", "all done\n> ", reviewerIdleWithoutSubmission},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{
				review: &domain.Review{
					ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1",
					ReviewerActivityState: domain.ActivityIdle, InterfaceMode: domain.ReviewerInterfaceTUI,
					UpdatedAt: now.Add(-reviewerIdleGrace),
				},
				runs: []domain.ReviewRun{{
					ID: "run-1", ReviewID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode,
					PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning,
					CreatedAt: now.Add(-reviewerIdleGrace - time.Minute),
				}},
			}
			fake := &fakeLauncher{alive: true}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: fake, screen: tc.screen})
			got, err := eng.List(context.Background(), "mer-1")
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			// A limit line with no usable reset gets a failure-time one appended.
			bodyOK := len(got.Runs) == 1 && (got.Runs[0].Body == tc.want ||
				(strings.HasSuffix(tc.want, "Usage limit reached") && strings.HasPrefix(got.Runs[0].Body, tc.want+" · resets ")))
			if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunFailed || !bodyOK {
				t.Fatalf("runs = %+v, want body %q", got.Runs, tc.want)
			}
			// A limited pane resumes on its own after the reset, so it is stopped and
			// forgotten before the sweep's retry starts the one reviewer for the head.
			limited := strings.HasPrefix(tc.want, ReviewerUsageLimitPrefix)
			if fake.destroyed != limited || (store.review.ReviewerHandleID == "") != limited {
				t.Fatalf("destroyed=%v handle=%q, want the pane stopped exactly for a limit wait", fake.destroyed, store.review.ReviewerHandleID)
			}
		})
	}
}

func TestListNamesTheTrustDialogWhenReviewerNeverStarted(t *testing.T) {
	store, _, _, launcher := watchdogFixture(time.Time{})
	launcher.screen = "Accessing workspace: /ws/w1\n Quick safety check: Is this a trusted folder?"
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || !strings.HasPrefix(got.Runs[0].Body, ReviewerNotStartedPrefix+"Claude is waiting on its workspace trust dialog") {
		t.Fatalf("runs = %+v, want a distinct trust-dialog failure", got.Runs)
	}
}

// The incident screen carries no reset the generic parser accepts, so the wait
// must come from the screen's clock (host zone) or, failing that, from the
// failure time: never from the pass start, which is long past by then.
func TestIdleLimitWaitHoldsUntilTheScreenResetOrFailureTimeFallback(t *testing.T) {
	prev := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prev })
	now := time.Date(2026, 11, 2, 13, 41, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, screen string
		want         time.Time
	}{
		{"screen clock", "[Usage limit reached; a short grace allowance remains.]\nUsage limit reached · continuing automatically at 5:20pm · esc to cancel", time.Date(2026, 11, 2, 17, 20, 0, 0, time.UTC)},
		{"clock already past rolls to tomorrow", "Usage limit reached · continuing automatically at 1:00pm", time.Date(2026, 11, 3, 13, 0, 0, 0, time.UTC)},
		{"no reset anywhere", "[Usage limit reached; a short grace allowance remains.]", now.Add(domain.UsageLimitFallbackPause)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, ok := usageLimitOnScreen(tc.screen, now)
			if !ok {
				t.Fatal("limit not found")
			}
			// The pass started well before the failure; the wait must not key off it.
			run := domain.ReviewRun{Status: domain.ReviewRunFailed, Body: ReviewerUsageLimitPrefix + body, CreatedAt: now.Add(-3 * time.Hour)}
			until, ok := UsageLimitPausedUntil(run)
			if !ok || !until.Equal(tc.want) {
				t.Fatalf("paused until %v (ok=%v), want %v; body %q", until, ok, tc.want, body)
			}
		})
	}
}

// A slow pane boot must not eat the launch grace: it counts from when the
// launch finished, not from when the pass was recorded.
func TestLaunchGraceCountsFromLaunchCompletion(t *testing.T) {
	store, _, eng, _ := watchdogFixture(time.Time{})
	eng.launchedAt["review-mer-1"] = eng.clock().Add(-10 * time.Second)
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Status != domain.ReviewRunRunning || store.review.ReviewerHandleID == "" {
		t.Fatalf("runs = %+v, want the pass still running inside the post-launch grace", got.Runs)
	}
}

// The failure text says what was on screen: blank, or unreadable.
func TestNeverStartedFailureNamesScreenState(t *testing.T) {
	for name, tc := range map[string]struct {
		screen, want string
		err          error
	}{
		"blank":      {screen: "  \n\n", want: "screen was blank"},
		"unreadable": {err: context.DeadlineExceeded, want: "could not be read"},
	} {
		t.Run(name, func(t *testing.T) {
			store, fake, _, _ := watchdogFixture(time.Time{})
			l := screenLauncher{fakeLauncher: fake, screen: tc.screen, err: tc.err}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, l)
			got, err := eng.List(context.Background(), "mer-1")
			if err != nil || len(got.Runs) != 1 || !strings.Contains(got.Runs[0].Body, tc.want) {
				t.Fatalf("err=%v runs=%+v, want body containing %q", err, got.Runs, tc.want)
			}
		})
	}
}

// B352: a reviewer pane that never reported a hook because it sat on the quota
// refusal (runs 06b393be, c655afc4, 52d721a2) is a usage-limit wait with the
// screen's reset, not "did not start".
func TestNeverStartedReviewerOnUsageLimitScreenWaitsForReset(t *testing.T) {
	store, fake, _, _ := watchdogFixture(time.Time{})
	screen := "You've hit your 5-hour limit · resets 5:20pm (Europe/Madrid)\n\nUsage limit reached again · continuing automatically at 5:20pm · esc to cancel\n"
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: fake, screen: screen})
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil || len(got.Runs) != 1 {
		t.Fatalf("err=%v runs=%+v", err, got.Runs)
	}
	run := got.Runs[0]
	if run.Status != domain.ReviewRunFailed || !strings.HasPrefix(run.Body, ReviewerUsageLimitPrefix) || strings.HasPrefix(run.Body, ReviewerNotStartedPrefix) {
		t.Fatalf("run = %+v, want a usage-limit failure", run)
	}
	if _, ok := UsageLimitPausedUntil(run); !ok {
		t.Fatalf("body %q has no parsable reset", run.Body)
	}
	if !IsUnsubmittedReviewerFailure(run.Body) || !fake.destroyed || store.review.ReviewerHandleID != "" {
		t.Fatalf("want a free retry and the limited pane released")
	}
}

// A screen carrying both a limit line and the trust text stays a limit wait.
func TestNeverStartedLimitScreenWithTrustTextStaysLimitWait(t *testing.T) {
	store, fake, _, _ := watchdogFixture(time.Time{})
	screen := "Do you trust the files in this trusted folder?\nUsage limit reached · continuing automatically at 5:20pm\n"
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, screenLauncher{fakeLauncher: fake, screen: screen})
	got, err := eng.List(context.Background(), "mer-1")
	if err != nil || len(got.Runs) != 1 || !strings.HasPrefix(got.Runs[0].Body, ReviewerUsageLimitPrefix) {
		t.Fatalf("err=%v runs=%+v, want a usage-limit body", err, got.Runs)
	}
}
