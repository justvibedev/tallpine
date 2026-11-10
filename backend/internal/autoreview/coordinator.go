// Package autoreview decides when the daemon may automatically review a
// worker's current pull-request heads.
package autoreview

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

const (
	// DefaultIdleThreshold is how long a worker must remain idle before an
	// automatic review may start.
	DefaultIdleThreshold = time.Minute
	// DefaultSweepInterval is the cadence for reevaluating live sessions.
	DefaultSweepInterval = time.Minute
	// DefaultStallThreshold is how long a PR head may need review with no
	// reviewer started before the orchestrator is told.
	DefaultStallThreshold = 10 * time.Minute
	// autoReviewFailedRetryLimit bounds retries for the same current PR head.
	autoReviewFailedRetryLimit = 3
)

// Store provides the durable session, project, PR, and review facts used by
// the coordinator.
type Store interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	ListLiveSessions(context.Context) ([]domain.SessionRecord, error)
	GetProject(context.Context, string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error)
	ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error)
}

// Trigger starts a source-tagged automatic review through the normal review
// engine.
type Trigger interface {
	TriggerAuto(context.Context, domain.SessionID, domain.ReviewerHarness) (reviewcore.TriggerResult, error)
}

// Result describes whether an evaluation started a review and why it skipped
// or triggered.
type Result struct {
	Triggered bool
	Reason    string
}

// Coordinator evaluates auto-review policy and owns the periodic sweep.
type Coordinator struct {
	store         Store
	reviews       Trigger
	clock         func() time.Time
	idleThreshold time.Duration
	sweepInterval time.Duration
	logger        *slog.Logger
	escalate      func(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, rounds int)

	stalled        func(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, waited time.Duration)
	stallThreshold time.Duration
	stallMu        sync.Mutex
	needsSince     map[string]time.Time
}

// Config customizes coordinator timing and logging.
type Config struct {
	Clock         func() time.Time
	IdleThreshold time.Duration
	SweepInterval time.Duration
	Logger        *slog.Logger
	// Escalate tells the project's orchestrator that a PR used up its allowed
	// automatic review rounds (ProjectConfig.AutoReviewMaxRounds). Optional; it
	// must be idempotent per PR and round count.
	Escalate func(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, rounds int)
	// Stalled tells the project's orchestrator that a PR head has sat in
	// needs_review with no reviewer started for StallThreshold (B389). Optional;
	// it must be idempotent per PR and head.
	Stalled        func(ctx context.Context, session domain.SessionRecord, prURL, headSHA string, waited time.Duration)
	StallThreshold time.Duration
}

// New constructs an auto-review coordinator.
func New(store Store, reviews Trigger, cfg Config) *Coordinator {
	c := &Coordinator{store: store, reviews: reviews, clock: cfg.Clock, idleThreshold: cfg.IdleThreshold, sweepInterval: cfg.SweepInterval, logger: cfg.Logger, escalate: cfg.Escalate, stalled: cfg.Stalled, stallThreshold: cfg.StallThreshold, needsSince: map[string]time.Time{}}
	if c.stallThreshold <= 0 {
		c.stallThreshold = DefaultStallThreshold
	}
	if c.clock == nil {
		c.clock = time.Now
	}
	if c.idleThreshold <= 0 {
		c.idleThreshold = DefaultIdleThreshold
	}
	if c.sweepInterval <= 0 {
		c.sweepInterval = DefaultSweepInterval
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	return c
}

// EvaluateSession evaluates one worker against the current project, activity,
// PR, and review-run facts.
func (c *Coordinator) EvaluateSession(ctx context.Context, id domain.SessionID) (Result, error) {
	return c.evaluate(ctx, id, false)
}

// EvaluateReady evaluates a worker that just reported a PR ready, without
// waiting for it to go idle. It does nothing unless the project opts in with
// AutoReviewOnReport (on by default; false opts out) and the session has auto-review enabled.
func (c *Coordinator) EvaluateReady(ctx context.Context, id domain.SessionID) (Result, error) {
	return c.evaluate(ctx, id, true)
}

func (c *Coordinator) evaluate(ctx context.Context, id domain.SessionID, ready bool) (Result, error) {
	session, ok, err := c.store.GetSession(ctx, id)
	if err != nil || !ok {
		if err == nil {
			return Result{Reason: "session_not_found"}, nil
		}
		return Result{}, err
	}
	project, ok, err := c.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return Result{}, err
	}
	if reason := sessionGate(session, reviewcore.AutoReviewActive(ctx, c.store, session, project, ok), c.clock(), c.idleThreshold, ready); reason != "" {
		return Result{Reason: reason}, nil
	}
	if !ok {
		return Result{Reason: "missing_reviewer_harness"}, nil
	}
	if ready && project.Config.AutoReviewOnReport != nil && !*project.Config.AutoReviewOnReport {
		if c.logger != nil {
			c.logger.Info("auto-review on report skipped", "session", id, "reason", "report_trigger_disabled")
		}
		return Result{Reason: "report_trigger_disabled"}, nil
	}
	harness := c.effectiveReviewerHarness(ctx, session, project.Config)
	if harness == "" {
		return Result{Reason: "missing_reviewer_harness"}, nil
	}
	prs, err := c.store.ListPRsBySession(ctx, id)
	if err != nil {
		return Result{}, err
	}
	runs, err := c.store.ListReviewRunsBySession(ctx, id)
	if err != nil {
		return Result{}, err
	}
	harnessRuns := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.Harness == harness || run.Harness == "" {
			harnessRuns = append(harnessRuns, run)
		}
	}
	states := reviewcore.Plan(prs, harnessRuns)
	if len(states) == 0 {
		return Result{Reason: "no_pr"}, nil
	}
	eligible := false
	hasRunning := false
	reason := "planner_ineligible"
	for _, state := range states {
		switch state.Status {
		case reviewcore.ReviewStateRunning:
			hasRunning = true
			reason = "review_running"
		case reviewcore.ReviewStateUpToDate:
			reason = "already_approved"
		case reviewcore.ReviewStateChangesRequested:
			reason = "changes_requested_same_sha"
		case reviewcore.ReviewStateIneligible:
			reason = ineligibleReason(prs, state.PRURL)
		case reviewcore.ReviewStateNeedsReview:
			if blocked, rounds := headBlockedReason(project.Config, harnessRuns, state); blocked != "" {
				if blocked == "review_rounds_exhausted" && c.escalate != nil {
					c.escalate(ctx, session, state.PRURL, state.TargetSHA, rounds)
				}
				reason = blocked
				continue
			}
			eligible = true
		}
	}
	if !eligible && !hasRunning {
		return Result{Reason: reason}, nil
	}
	result, err := c.reviews.TriggerAuto(ctx, id, harness)
	if err != nil {
		return Result{}, fmt.Errorf("trigger auto review for %s: %w", id, err)
	}
	if result.SkipReason != "" {
		return Result{Reason: result.SkipReason}, nil
	}
	if !result.Created {
		return Result{Reason: triggerResultReason(prs, result, harness)}, nil
	}
	return Result{Triggered: true, Reason: "triggered"}, nil
}

// headBlockedReason says why a needs_review head is deliberately not reviewed
// automatically (empty when it is due): a same-head run that settles it, or the
// project's round limit. evaluate and the stall watch share it, so a stall is
// only ever a head that should have started.
// retryLimitReason is the one blocked-head reason that is a failure rather than
// a deliberate stop, so the stall watch still reports it.
const retryLimitReason = "failed_same_sha_retry_limit"

func headBlockedReason(cfg domain.ProjectConfig, harnessRuns []domain.ReviewRun, state reviewcore.PRReviewState) (reason string, rounds int) {
	if blocked := existingHeadReason(harnessRuns, state.PRURL, state.TargetSHA); blocked != "" {
		return blocked, 0
	}
	if limit := cfg.AutoReviewMaxRounds; limit > 0 {
		if rounds := reviewcore.ChangesRequestedRounds(harnessRuns, state.PRURL); rounds >= limit {
			return "review_rounds_exhausted", rounds
		}
	}
	return "", 0
}

func triggerResultReason(prs []domain.PullRequest, result reviewcore.TriggerResult, harness domain.ReviewerHarness) string {
	for _, state := range result.Reviews {
		if state.Status == reviewcore.ReviewStateRunning {
			return "review_running"
		}
	}
	runs := make([]domain.ReviewRun, 0, len(result.Runs))
	for _, run := range result.Runs {
		if run.Harness == harness || run.Harness == "" {
			runs = append(runs, run)
		}
	}
	for _, pr := range prs {
		if blocked := existingHeadReason(runs, pr.URL, pr.HeadSHA); blocked != "" {
			return blocked
		}
	}
	return "planner_up_to_date"
}

func existingHeadReason(runs []domain.ReviewRun, prURL, targetSHA string) string {
	retries := 0
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA {
			continue
		}
		// A pass that died with its reviewer, like a failed auto pass, leaves
		// the head eligible until the retry limit.
		if reviewcore.CountsTowardAutoRetryLimit(run) {
			retries++
			continue
		}
		if run.Status == domain.ReviewRunRunning {
			return "review_running"
		}
		if run.Status == domain.ReviewRunCancelled {
			return "cancelled_same_sha"
		}
		if run.Verdict == domain.VerdictApproved {
			return "already_approved"
		}
		if run.Verdict == domain.VerdictChangesRequested {
			return "changes_requested_same_sha"
		}
	}
	if retries >= autoReviewFailedRetryLimit {
		return retryLimitReason
	}
	return ""
}

func (c *Coordinator) effectiveReviewerHarness(ctx context.Context, session domain.SessionRecord, config domain.ProjectConfig) domain.ReviewerHarness {
	harness := session.ReviewerHarness
	if harness == "" {
		var app domain.WorkflowDefaults
		if s, ok := c.store.(interface {
			WorkflowDefaults(context.Context) (domain.WorkflowDefaults, error)
		}); ok {
			d, err := s.WorkflowDefaults(ctx)
			if err != nil {
				c.logger.Warn("auto-review could not read app default reviewer", "session", session.ID, "error", err)
			}
			app = d
		}
		harness, _ = config.EffectiveReviewer(app, session.Harness)
	}
	// Mirror review.Engine: a saved reviewer that was removed resolves to the fallback.
	resolved, _ := harness.OrFallback()
	return resolved
}

func ineligibleReason(prs []domain.PullRequest, url string) string {
	for _, pr := range prs {
		if pr.URL != url {
			continue
		}
		switch {
		case pr.Draft:
			return "draft_pr"
		case pr.Merged:
			return "merged_pr"
		case pr.Closed:
			return "closed_pr"
		case pr.HeadSHA == "":
			return "missing_head_sha"
		default:
			return "planner_ineligible"
		}
	}
	return "planner_ineligible"
}

func sessionGate(session domain.SessionRecord, enabled bool, now time.Time, threshold time.Duration, ready bool) string {
	if !enabled {
		return "disabled"
	}
	if session.Kind != domain.KindWorker {
		return "not_worker"
	}
	if session.IsTerminated {
		return "terminated"
	}
	if ready {
		// The worker filed its report as its last act; its activity state may
		// not have settled to idle yet.
		return ""
	}
	if session.Activity.State != domain.ActivityIdle {
		return "not_idle"
	}
	if session.Activity.LastActivityAt.IsZero() || now.Sub(session.Activity.LastActivityAt) < threshold {
		return "idle_threshold_not_met"
	}
	return ""
}

// Sweep evaluates every live session, isolating per-session failures. A
// terminated session never qualifies, so the sweep does not read history.
func (c *Coordinator) Sweep(ctx context.Context) error {
	sessions, err := c.store.ListLiveSessions(ctx)
	if err != nil {
		return err
	}
	for _, session := range sessions {
		if session.Kind != domain.KindWorker || session.IsTerminated {
			continue
		}
		c.syncAutoReviewFlag(ctx, session)
		if _, err := c.EvaluateSession(ctx, session.ID); err != nil {
			c.logger.Error("auto-review: evaluate session failed", "session_id", session.ID, "err", err)
		}
		c.watchStall(ctx, session)
	}
	return nil
}

// Start evaluates persisted session and PR facts on the configured cadence
// until ctx is cancelled.
func (c *Coordinator) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(c.sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := c.Sweep(ctx); err != nil {
					c.logger.Error("auto-review: sweep failed", "err", err)
				}
			}
		}
	}()
	return done
}

// watchStall reports a PR head that has needed review, with no reviewer running
// or finished on it, for longer than the stall threshold, while Auto review is on for the
// worker and it is past the idle gate (e.g. retries exhausted, a failed launch). A head is reported once.
func (c *Coordinator) watchStall(ctx context.Context, session domain.SessionRecord) {
	if c.stalled == nil {
		return
	}
	now := c.clock()
	c.stallMu.Lock()
	defer c.stallMu.Unlock()
	prefix := string(session.ID) + "|"
	forget := func() {
		for key := range c.needsSince {
			if strings.HasPrefix(key, prefix) {
				delete(c.needsSince, key)
			}
		}
	}
	// Only a review that should have started counts: Auto review on for this
	// worker and the worker past the idle gate.
	project, found, err := c.store.GetProject(ctx, string(session.ProjectID))
	if err != nil || sessionGate(session, reviewcore.AutoReviewActive(ctx, c.store, session, project, found), now, c.idleThreshold, false) != "" {
		forget()
		return
	}
	prs, err := c.store.ListPRsBySession(ctx, session.ID)
	if err != nil {
		return
	}
	runs, err := c.store.ListReviewRunsBySession(ctx, session.ID)
	if err != nil {
		return
	}
	live := map[string]bool{}
	harness := c.effectiveReviewerHarness(ctx, session, project.Config)
	harnessRuns := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.Harness == harness || run.Harness == "" {
			harnessRuns = append(harnessRuns, run)
		}
	}
	for _, state := range reviewcore.Plan(prs, harnessRuns) {
		if state.Status != reviewcore.ReviewStateNeedsReview {
			continue
		}
		if blocked, _ := headBlockedReason(project.Config, harnessRuns, state); (blocked != "" && blocked != retryLimitReason) || headReviewedByAnyAgent(runs, state) {
			continue
		}
		key := string(session.ID) + "|" + state.PRURL + "|" + state.TargetSHA
		live[key] = true
		since, seen := c.needsSince[key]
		if !seen {
			c.needsSince[key] = now
			continue
		}
		if since.IsZero() || now.Sub(since) < c.stallThreshold {
			continue
		}
		c.needsSince[key] = time.Time{} // reported; stays tracked so it is not re-sent
		c.stalled(ctx, session, state.PRURL, state.TargetSHA, now.Sub(since))
	}
	for key := range c.needsSince {
		if strings.HasPrefix(key, prefix) && !live[key] {
			delete(c.needsSince, key)
		}
	}
}

// flagSyncer rewrites a session's stored Auto review flag without recording a
// per-session override.
type flagSyncer interface {
	SyncSessionAutoReviewFlag(ctx context.Context, id domain.SessionID, enabled bool, at time.Time) (bool, error)
}

// syncAutoReviewFlag keeps the stored flag, which the session API, kanban and
// UI read, equal to what the gates decide. A project or app toggle thus shows
// on already-running workers within a sweep, and an override already equals its
// flag, so only follow-the-setting sessions change.
func (c *Coordinator) syncAutoReviewFlag(ctx context.Context, session domain.SessionRecord) {
	syncer, ok := c.store.(flagSyncer)
	if !ok {
		return
	}
	project, found, err := c.store.GetProject(ctx, string(session.ProjectID))
	if err != nil {
		return
	}
	if active := reviewcore.AutoReviewActive(ctx, c.store, session, project, found); active != session.AutoReviewEnabled {
		if _, err := syncer.SyncSessionAutoReviewFlag(ctx, session.ID, active, c.clock()); err != nil {
			c.logger.Warn("auto-review: sync session flag failed", "session_id", session.ID, "err", err)
		}
	}
}

// headReviewedByAnyAgent reports a running or finished review on the head from
// any reviewer, such as the stronger risk reviewer, which the configured
// reviewer's own plan does not see.
func headReviewedByAnyAgent(runs []domain.ReviewRun, state reviewcore.PRReviewState) bool {
	for _, run := range runs {
		if run.PRURL != state.PRURL || run.TargetSHA != state.TargetSHA {
			continue
		}
		if run.Status == domain.ReviewRunRunning || run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested {
			return true
		}
	}
	return false
}
