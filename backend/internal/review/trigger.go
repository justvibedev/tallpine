package review

import (
	stdctx "context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TriggerResult is the outcome of a trigger: the (new or existing) run, the live
// reviewer pane's handle so the UI can attach its terminal, and whether a new
// pass was started (false when an existing run for the same commit was reused).
type TriggerResult struct {
	Run              domain.ReviewRun
	ReviewerHandleID string
	Created          bool
	// Queued is set when the pass could not start because the reviewer is busy
	// and was parked to start once it is free; nothing was created.
	Queued          bool
	Reviews         []PRReviewState
	Runs            []domain.ReviewRun
	CreatedRuns     []domain.ReviewRun
	ReviewerSurface domain.ReviewerSurface
	// SkipReason is set only for a normal automatic-trigger policy race, such
	// as the worker becoming active after the coordinator's initial read.
	SkipReason string
}

// SessionReviews is a worker's review state: the live reviewer handle plus its
// recorded passes, newest first.
type SessionReviews struct {
	ReviewerHandleID      string
	ReviewerHarness       domain.ReviewerHarness
	ReviewerActivityState domain.ActivityState
	Runs                  []domain.ReviewRun
	Reviews               []PRReviewState
	ReviewerSurface       domain.ReviewerSurface
	// ActiveReviewers lists every reviewer with a live pane or running pass,
	// selected reviewer first, so clients can open each of several reviewers
	// working on the same worker at once.
	ActiveReviewers []domain.ReviewerSurface
	// Queued lists the passes waiting for the reviewer to be free.
	Queued []domain.QueuedReviewTrigger
}

// CancelResult is the review state after a reviewer pane cancellation.
type CancelResult struct {
	ReviewerHandleID string
	Reviews          []PRReviewState
	CancelledRuns    []domain.ReviewRun
}

// TerminateResult reports the reviewer pane hard-teardown performed because
// the owning worker session is leaving its live lifecycle.
type TerminateResult struct {
	ReviewerHandleID string
	CancelledRuns    []domain.ReviewRun
}

// RestoreReviewerResult reports an idle reviewer pane restored alongside its
// worker session.
type RestoreReviewerResult struct {
	ReviewerHandleID string
	Restored         bool
}

// TriggerOptions selects the reviewer, its surface, and the same-commit policy
// for one trigger.
type TriggerOptions struct {
	// Harness and Config override the resolved reviewer for this pass only.
	Harness domain.ReviewerHarness
	Config  domain.AgentConfig
	Source  domain.ReviewTriggerSource
	// InterfaceMode chooses Chat or Terminal for the reviewer; empty keeps the
	// reviewer's persisted surface or the adapter default.
	InterfaceMode domain.ReviewerInterfaceMode
	// PRURL restricts the pass to one of the worker's PRs; empty reviews every
	// eligible PR on the session.
	PRURL string
	// RejectReviewedHead turns "nothing new to review" into an error instead of
	// a silent reuse: ErrReviewAlreadyRunning when a pass is already running on
	// a PR head, ErrHeadAlreadyReviewed when every head already has a review.
	RejectReviewedHead bool
	// Rerun reviews every open PR head again even when it already has a review,
	// and lets a different reviewer run alongside one that is still running.
	// The same reviewer is never started twice on one head at the same time:
	// a person's rerun reuses that running pass, an agent's rerun is told so.
	Rerun bool
	// RetryOfRunID names the failed run an automatic retry replaces. The pass
	// is skipped when that reviewer already has another pass on the commit by
	// the time the worker lock is held.
	RetryOfRunID string
	// QueuedSince marks a rerun that waited in the trigger queue since then:
	// it is skipped when the reviewer already reviewed the head after that.
	QueuedSince time.Time
}

// TriggerWithOptions starts reviews for every PR on the worker session that needs review.
// It reuses running/up-to-date runs, retries failed/current changes-requested
// heads, and uses one reviewer pane for every new run in the batch. Automatic
// retries against the same PR head stop after three failed auto-review runs.
//
// An empty override keeps the project's configured reviewer. A known one runs
// this pass under it without editing project config, so picking a reviewer for
// one session cannot change what any other session in the project runs. The
// harness-change path below already handles the swap by respawning the pane.
func (e *Engine) TriggerWithOptions(ctx stdctx.Context, workerID domain.SessionID, opts TriggerOptions) (TriggerResult, error) {
	source, err := validateTriggerRequest(workerID, opts)
	if err != nil {
		return TriggerResult{}, err
	}

	// Serialise concurrent triggers for this worker so the idempotency check
	// below (and the reviewer spawn that follows it) can't be raced into a
	// double-spawn. Held across the spawn deliberately: the loser then re-reads
	// the freshly-recorded run and short-circuits to Created:false.
	if err := e.waitForChatRecovery(ctx); err != nil {
		return TriggerResult{}, err
	}
	unlock := e.lockWorker(workerID)
	defer unlock()

	p := &triggerPass{e: e, ctx: ctx, workerID: workerID, opts: opts, source: source, mode: opts.InterfaceMode}
	defer p.releaseHarnessUse()
	for _, step := range []func() (TriggerResult, bool, error){
		p.loadWorker,
		p.loadPRsAndRuns,
		p.skipStaleRetry,
		p.resolveReviewer,
		p.reconcileRuns,
		p.carryApprovals,
		p.planReviews,
		p.recordRuns,
		p.reuseWhenNothingDue,
		p.launchReviewer,
	} {
		if res, done, err := step(); done || err != nil {
			return res, err
		}
	}
	return p.result(), nil
}

// validateTriggerRequest rejects malformed requests before any state is read
// and returns the effective trigger source (manual when none is given).
func validateTriggerRequest(workerID domain.SessionID, opts TriggerOptions) (domain.ReviewTriggerSource, error) {
	override, source, mode := opts.Harness, opts.Source, opts.InterfaceMode
	if source == "" {
		source = domain.ReviewTriggerManual
	}
	if (opts.Rerun || opts.RejectReviewedHead) && source == domain.ReviewTriggerAuto {
		return "", fmt.Errorf("%w: automatic review cannot request a rerun or same-commit rejection", ErrInvalid)
	}
	if workerID == "" {
		return "", fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if override != "" && !override.IsKnown() {
		return "", fmt.Errorf("%w: unknown reviewer harness %q", ErrInvalid, override)
	}
	switch source {
	case domain.ReviewTriggerManual, domain.ReviewTriggerAgent, domain.ReviewTriggerAuto:
	default:
		return "", fmt.Errorf("%w: unknown review trigger source %q", ErrInvalid, source)
	}
	if mode != "" && mode != domain.ReviewerInterfaceChat && mode != domain.ReviewerInterfaceTUI {
		return "", fmt.Errorf("%w: unknown reviewer interface mode %q", ErrInvalid, mode)
	}
	if source == domain.ReviewTriggerAuto && mode != "" {
		return "", fmt.Errorf("%w: automatic review cannot override interface mode", ErrInvalid)
	}
	return source, nil
}

func autoReviewSessionReason(worker domain.SessionRecord, enabled bool, now time.Time) string {
	switch {
	case !enabled:
		return "disabled"
	case worker.Kind != domain.KindWorker:
		return "not_worker"
	case worker.IsTerminated:
		return "terminated"
	case worker.Activity.State != domain.ActivityIdle:
		return "not_idle"
	case worker.Activity.LastActivityAt.IsZero() || now.Sub(worker.Activity.LastActivityAt) < time.Minute:
		return "idle_threshold_not_met"
	default:
		return ""
	}
}

// headHasReview reports whether any reviewer has a running or completed pass
// on the PR head. Failed and cancelled passes never produced a review.
func headHasReview(runs []domain.ReviewRun, prURL, targetSHA string) bool {
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA {
			continue
		}
		if run.Status == domain.ReviewRunRunning || run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested {
			return true
		}
	}
	return false
}

func headRunningForHarness(runs []domain.ReviewRun, prURL, targetSHA string, harness domain.ReviewerHarness) bool {
	for _, run := range runs {
		if run.PRURL == prURL && run.TargetSHA == targetSHA && run.Status == domain.ReviewRunRunning && (run.Harness == harness || run.Harness == "") {
			return true
		}
	}
	return false
}

// nothingToReviewError explains why a trigger that asked not to reuse silently
// started nothing. It names the first reviewable PR head, so the caller sees
// which commit is already covered and what to do next.
func nothingToReviewError(reviews []PRReviewState, runs []domain.ReviewRun, harness domain.ReviewerHarness, rerun bool) error {
	for _, review := range reviews {
		if review.Status == ReviewStateIneligible {
			continue
		}
		sha := shortSHA(review.TargetSHA)
		if rerun {
			// One reviewer reviews one PR of the session at a time. When it is
			// busy on another PR, say so: naming this PR read as a phantom run,
			// since `review ls` shows no pass on it (B393).
			if busy, ok := runningRunForHarness(runs, harness); ok && busy.PRURL != review.PRURL {
				return fmt.Errorf("%w: %s is busy reviewing %s head %s, so PR #%d head %s cannot start yet; wait for that pass, cancel it with `tallpine review cancel`, or choose another reviewer with --agent", ErrReviewAlreadyRunning, harness, busyPRLabel(reviews, busy.PRURL), shortSHA(busy.TargetSHA), review.PRNumber, sha)
			}
			return fmt.Errorf("%w: %s is already reviewing PR #%d head %s; wait for it, cancel it with `tallpine review cancel`, or choose another reviewer with --agent", ErrReviewAlreadyRunning, harness, review.PRNumber, sha)
		}
		for _, run := range runs {
			if run.PRURL == review.PRURL && run.TargetSHA == review.TargetSHA && run.Status == domain.ReviewRunRunning {
				return fmt.Errorf("%w: %s is already reviewing PR #%d head %s; wait for its result, or pass --rerun to add another reviewer", ErrReviewAlreadyRunning, run.Harness, review.PRNumber, sha)
			}
		}
		verdict := "reviewed"
		if review.LatestRun != nil && review.LatestRun.Verdict != domain.VerdictNone {
			verdict = string(review.LatestRun.Verdict)
		}
		return fmt.Errorf("%w: PR #%d head %s was already reviewed (%s); push new commits, or pass --rerun to review this commit again. If you just pushed, wait a few seconds and run it again", ErrHeadAlreadyReviewed, review.PRNumber, sha, verdict)
	}
	return fmt.Errorf("%w: no open PR head to review", ErrInvalid)
}

// runningRunForHarness returns the pass this reviewer is running, if any.
// Legacy rows without a harness belong to whichever reviewer is current.
func runningRunForHarness(runs []domain.ReviewRun, harness domain.ReviewerHarness) (domain.ReviewRun, bool) {
	for _, run := range runs {
		if run.Status == domain.ReviewRunRunning && (run.Harness == harness || run.Harness == "") {
			return run, true
		}
	}
	return domain.ReviewRun{}, false
}

func busyPRLabel(reviews []PRReviewState, prURL string) string {
	for _, review := range reviews {
		if review.PRURL == prURL {
			return fmt.Sprintf("PR #%d", review.PRNumber)
		}
	}
	return prURL
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
