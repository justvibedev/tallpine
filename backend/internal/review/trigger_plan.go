package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// staleSupersede names a PR head whose stale running runs are superseded once
// the replacement reviewer has started.
type staleSupersede struct {
	prURL     string
	targetSHA string
}

// triggerPass holds what one TriggerWithOptions call learns and decides, so the
// steps (eligibility, run recording, launch) can each be read and tested alone.
// The fields were locals of one long function; the steps keep their order.
type triggerPass struct {
	e        *Engine
	ctx      stdctx.Context
	workerID domain.SessionID
	opts     TriggerOptions
	source   domain.ReviewTriggerSource
	mode     domain.ReviewerInterfaceMode

	worker domain.SessionRecord
	prs    []domain.PullRequest
	runs   []domain.ReviewRun

	harness               domain.ReviewerHarness
	config                domain.AgentConfig
	hasHarnessOverride    bool
	hasConfigOverride     bool
	secondOpinionOverride bool
	reviewRows            []domain.Review
	reviewRow             domain.Review
	previousReview        domain.Review
	hasReview             bool
	selectedMode          domain.ReviewerInterfaceMode
	modeChanging          bool
	hadRunningReviewer    bool
	reviews               []PRReviewState
	modelReason           string
	now                   time.Time

	created           []domain.ReviewRun
	restarted         []domain.ReviewRun
	pendingSupersedes []staleSupersede
	batchID           string

	// Launch state, see trigger_launch.go.
	previousHandleID        string
	previousAgentSessionID  string
	previousLaunchID        string
	deferInitialMessage     bool
	handleID                string
	previousStopped         bool
	queueRuns               []domain.ReviewRun
	queue                   []ports.ReviewTask
	launchRun               domain.ReviewRun
	launchAgentSessionID    string
	persistedAgentSessionID string
	releaseHarness          func()
}

func (p *triggerPass) releaseHarnessUse() {
	if p.releaseHarness != nil {
		p.releaseHarness()
	}
}

// loadWorker reads the worker and applies the session-level skips and refusals.
func (p *triggerPass) loadWorker() (TriggerResult, bool, error) {
	worker, ok, err := p.e.sessions.GetSession(p.ctx, p.workerID)
	if err != nil {
		return TriggerResult{}, false, err
	}
	if !ok {
		return TriggerResult{}, false, fmt.Errorf("%w: worker session %q", ErrNotFound, p.workerID)
	}
	if p.source == domain.ReviewTriggerAuto {
		if reason := autoReviewSessionReason(worker, p.e.clock()); reason != "" {
			return TriggerResult{SkipReason: reason}, true, nil
		}
	}
	if worker.IsTerminated {
		return TriggerResult{}, false, fmt.Errorf("%w: worker session %q is terminated", ErrInvalid, p.workerID)
	}
	if worker.Metadata.WorkspacePath == "" {
		return TriggerResult{}, false, fmt.Errorf("%w: worker session %q has no workspace to review", ErrInvalid, p.workerID)
	}
	p.worker = worker
	return TriggerResult{}, false, nil
}

// loadPRsAndRuns reads the worker's PRs (narrowed to opts.PRURL) and its runs.
func (p *triggerPass) loadPRsAndRuns() (TriggerResult, bool, error) {
	prs, err := p.e.prs.ListPRsBySession(p.ctx, p.workerID)
	if err != nil {
		return TriggerResult{}, false, err
	}
	if len(prs) == 0 {
		local, ok, err := LocalTarget(p.ctx, p.worker)
		if errors.Is(err, ErrNoLocalCommits) {
			return TriggerResult{}, false, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		if err != nil {
			return TriggerResult{}, false, err
		}
		if ok {
			prs = []domain.PullRequest{local}
		}
	}
	if len(prs) == 0 {
		return TriggerResult{}, false, fmt.Errorf("%w: Tallpine is not tracking a PR for worker %q yet; if you just opened one, run `tallpine review trigger --pr <url>`", ErrInvalid, p.workerID)
	}
	if want := strings.TrimSpace(p.opts.PRURL); want != "" {
		// A targeted pass reviews only the named PR, matched by API or web URL.
		var only []domain.PullRequest
		for _, pr := range prs {
			if pr.URL == want || pr.HTMLURL == want {
				only = append(only, pr)
			}
		}
		if len(only) == 0 {
			return TriggerResult{}, false, fmt.Errorf("%w: pull request %s is not tracked for worker %q", ErrNotFound, want, p.workerID)
		}
		prs = only
	}
	runs, err := p.e.store.ListReviewRunsBySession(p.ctx, p.workerID)
	if err != nil {
		return TriggerResult{}, false, err
	}
	p.prs, p.runs = prs, runs
	return TriggerResult{}, false, nil
}

// resolveReviewerConfig applies a per-pass override to the project's resolved
// reviewer. It reports whether the pass changes the configuration.
func resolveReviewerConfig(harness domain.ReviewerHarness, config domain.AgentConfig, override domain.ReviewerHarness, overrideConfig domain.AgentConfig) (domain.ReviewerHarness, domain.AgentConfig, bool) {
	resolvedHarness, resolvedConfig := harness, config
	hasConfigOverride := false
	if override != "" {
		harness = override
		if override == resolvedHarness {
			config = mergeReviewerAgentConfig(resolvedConfig, overrideConfig)
			hasConfigOverride = config != resolvedConfig
		} else if !overrideConfig.IsZero() {
			config = mergeReviewerAgentConfig(domain.AgentConfig{}, overrideConfig)
		} else {
			config = domain.AgentConfig{}
		}
	} else if !overrideConfig.IsZero() {
		config = mergeReviewerAgentConfig(config, overrideConfig)
		hasConfigOverride = config != resolvedConfig
	}
	return harness, config, hasConfigOverride
}

// resolveReviewer picks the harness, config and surface for this pass.
func (p *triggerPass) resolveReviewer() (TriggerResult, bool, error) {
	e, ctx, workerID := p.e, p.ctx, p.workerID
	if err := p.opts.Config.Validate(); err != nil {
		return TriggerResult{}, false, fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
	}
	harness, config, err := e.reviewerSelection(ctx, p.worker)
	if err != nil {
		return TriggerResult{}, false, err
	}
	p.hasHarnessOverride = p.opts.Harness != ""
	p.harness, p.config, p.hasConfigOverride = resolveReviewerConfig(harness, config, p.opts.Harness, p.opts.Config)
	if err := e.checkModelPolicy(ctx, p.worker.ProjectID, p.config.Model); err != nil {
		return TriggerResult{}, false, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	p.dropReviewedHeads(harness)
	p.applyRiskReviewer(harness)
	reviewRows, err := e.store.ListReviewsBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, false, err
	}
	reviewRow, hasReview, err := e.store.GetReviewBySessionAndHarness(ctx, workerID, p.harness)
	if err != nil {
		return TriggerResult{}, false, err
	}
	p.reviewRows, p.reviewRow, p.hasReview = reviewRows, reviewRow, hasReview
	p.previousReview = reviewRow
	p.selectedMode = p.mode
	if p.selectedMode == "" && hasReview {
		p.selectedMode = reviewRow.InterfaceMode
	}
	if p.selectedMode == "" {
		p.selectedMode = e.launcher.InterfaceMode(p.harness)
	}
	if p.selectedMode == domain.ReviewerInterfaceChat && e.launcher.InterfaceMode(p.harness) != domain.ReviewerInterfaceChat {
		if p.mode != "" {
			return TriggerResult{}, false, fmt.Errorf("%w: reviewer %q does not support Chat", ErrInvalid, p.harness)
		}
		p.selectedMode = domain.ReviewerInterfaceTUI
	}
	return TriggerResult{}, false, nil
}

// reconcileRuns releases idle panes of other reviewers and settles runs that
// can no longer be live, so the plan below sees the truth.
func (p *triggerPass) reconcileRuns() (TriggerResult, bool, error) {
	e, ctx, workerID, harness := p.e, p.ctx, p.workerID, p.harness
	// Another reviewer that is still working keeps running: a worker may ask
	// several reviewers for opinions on the same commit at once. Only idle
	// panes of other reviewers are released.
	if err := e.destroyIdleOtherReviewerHandles(ctx, workerID, harness, p.reviewRows, p.runs); err != nil {
		return TriggerResult{}, false, err
	}
	p.modeChanging = p.hasReview && p.reviewRow.InterfaceMode != "" && p.reviewRow.InterfaceMode != p.selectedMode
	p.secondOpinionOverride = p.hasConfigOverride || (p.modeChanging && p.mode != "")
	if p.modeChanging {
		p.hasConfigOverride = true
	}
	if stale, err := e.cancelStaleRunningRuns(ctx, workerID, harness, p.reviewRow, p.hasReview, p.runs); err != nil {
		return TriggerResult{}, false, err
	} else if stale {
		runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
		if err != nil {
			return TriggerResult{}, false, err
		}
		p.runs = runs
	}
	// Runs still running here belong to a live reviewer. One whose turn ended
	// without a submit would otherwise hold this harness's slot forever.
	if p.hasReview {
		if failed, err := e.failUnsubmittedReviewerRuns(ctx, p.reviewRow, runningRunsForReview(p.runs, p.reviewRow.ID)); err != nil {
			return TriggerResult{}, false, err
		} else if failed {
			runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
			if err != nil {
				return TriggerResult{}, false, err
			}
			p.runs = runs
		}
	}
	return TriggerResult{}, false, nil
}

// planReviews builds the per-PR plan and returns early when this reviewer is
// already working.
func (p *triggerPass) planReviews() (TriggerResult, bool, error) {
	harness, source, opts := p.harness, p.source, p.opts
	p.hadRunningReviewer = reviewRunsContainRunningForHarness(p.runs, harness)
	p.reviews = Plan(p.prs, p.runs)
	if source == domain.ReviewTriggerAuto || opts.Rerun {
		p.reviews = Plan(p.prs, reviewRunsForHarness(p.runs, harness))
		if source == domain.ReviewTriggerAuto {
			p.excludeExhaustedPRs(reviewRunsForHarness(p.runs, harness))
		}
		// Automatic sweeps may discover another eligible PR while this harness is
		// already reviewing one. Reconciliation above has proved the reviewer
		// terminal is still alive, so do not append work to its active batch. A
		// later sweep can start the remaining PR after the current run terminates.
		if p.hadRunningReviewer {
			// A person's rerun reuses the pass already running for this reviewer;
			// an agent asked for new work and must hear that none started.
			if opts.Rerun && source == domain.ReviewTriggerAgent {
				return TriggerResult{}, false, nothingToReviewError(p.reviews, p.runs, harness, true)
			}
			return TriggerResult{
				Run:              firstReusableRun(p.reviews),
				ReviewerHandleID: legacyReviewerHandle(p.reviewRow),
				Created:          false,
				Reviews:          p.reviews,
				Runs:             p.runs,
				ReviewerSurface:  reviewerSurface(p.reviewRow),
			}, true, nil
		}
	}

	return TriggerResult{}, false, nil
}

// markReviewerActive records the reviewer row as active. Only a trigger that
// starts a pass marks the reviewer active. Hooks write its activity without the
// worker lock, and the unsubmitted-turn expiry reads the idle-since time, so a
// trigger that starts nothing must not touch either.
func (p *triggerPass) markReviewerActive() error {
	var err error
	p.reviewRow, err = p.e.upsertReview(p.ctx, p.worker, p.harness, p.reviewRow.ReviewerHandleID, p.reviewRow.AgentSessionID, p.reviewRow.ReviewerLaunchID, domain.ActivityActive, p.now)
	return err
}

// prDueForTrigger reports whether a PR's head needs a pass from this trigger.
func prDueForTrigger(state PRReviewState, runs []domain.ReviewRun, harness domain.ReviewerHarness, source domain.ReviewTriggerSource, opts TriggerOptions, hasHarnessOverride, secondOpinionOverride bool) bool {
	// A PR that is already up to date has nothing due — unless the caller asked
	// for a different reviewer than the one that produced that verdict. Picking
	// another agent is precisely a request for a second opinion on this commit,
	// so refusing it makes the reviewer choice inert exactly when it is most
	// useful. Ineligible PRs stay excluded: nothing can review those.
	eligible := state.Status == ReviewStateNeedsReview || (source != domain.ReviewTriggerAuto && state.Status == ReviewStateChangesRequested) || (opts.Rerun && state.Status != ReviewStateIneligible)
	if source == domain.ReviewTriggerAuto && autoReviewHeadBlocked(runs, state.PRURL, state.TargetSHA, harness) {
		eligible = false
	}
	switch {
	case state.Status == ReviewStateIneligible:
		return false
	case opts.Rerun:
		// An explicit rerun reviews the head again, except that one reviewer
		// is never started twice on the same head concurrently.
		return !headRunningForHarness(runs, state.PRURL, state.TargetSHA, harness)
	case opts.RejectReviewedHead:
		// Any pass on this head, running or complete, by any reviewer, means
		// it is not due; the caller asked to hear about that, not reuse it.
		return !headHasReview(runs, state.PRURL, state.TargetSHA)
	case !eligible && !secondOpinionWanted(state, hasHarnessOverride, secondOpinionOverride, harness):
		return false
	}
	return true
}

// recordRuns inserts a running run for every PR that is due.
func (p *triggerPass) recordRuns() (TriggerResult, bool, error) {
	e, ctx, workerID, harness := p.e, p.ctx, p.workerID, p.harness
	p.now = e.clock()
	// A new reviewer row is recorded up front so runs get its id.
	if !p.hasReview {
		if err := p.markReviewerActive(); err != nil {
			return TriggerResult{}, false, err
		}
	}
	for _, reviewState := range p.reviews {
		if !prDueForTrigger(reviewState, p.runs, harness, p.source, p.opts, p.hasHarnessOverride, p.secondOpinionOverride) {
			continue
		}
		if p.hasConfigOverride {
			p.pendingSupersedes = append(p.pendingSupersedes, staleSupersede{prURL: reviewState.PRURL, targetSHA: reviewState.TargetSHA})
			if reviewState.LatestRun != nil && reviewState.LatestRun.Status == domain.ReviewRunRunning && (reviewState.LatestRun.Harness == harness || reviewState.LatestRun.Harness == "") {
				p.restarted = append(p.restarted, *reviewState.LatestRun)
				continue
			}
		} else if _, err := e.store.SupersedeStaleRunningReviewRuns(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA, reviewRunSupersededReason); err != nil {
			return TriggerResult{}, false, err
		}
		if p.batchID == "" {
			p.batchID = e.newID()
		}
		run := domain.ReviewRun{
			ID:            e.newID(),
			ReviewID:      p.reviewRow.ID,
			SessionID:     workerID,
			BatchID:       p.batchID,
			Harness:       harness,
			Model:         strings.TrimSpace(p.config.Model),
			ModelReason:   p.modelReason,
			TriggerSource: p.source,
			PRURL:         reviewState.PRURL,
			TargetSHA:     reviewState.TargetSHA,
			Status:        domain.ReviewRunRunning,
			Verdict:       domain.VerdictNone,
			CreatedAt:     p.now,
			// Completion refreshes this snapshot before delivery. Keeping the
			// trigger-time value also makes a running pass truthful in the API.
			AutoInjectReview: p.worker.AutoInjectReview,
		}
		if err := e.store.InsertReviewRun(ctx, run); err != nil {
			if errors.Is(err, domain.ErrDuplicateReviewRun) {
				if existing, ok, getErr := e.store.GetReviewRunBySessionPRSHAAndHarness(ctx, workerID, reviewState.PRURL, reviewState.TargetSHA, harness); getErr != nil {
					return TriggerResult{}, false, getErr
				} else if ok {
					p.reviews = replaceReviewLatestRun(p.reviews, reviewState.PRURL, reviewState.TargetSHA, existing)
					continue
				}
			}
			return TriggerResult{}, false, err
		}
		p.created = append(p.created, run)
		p.reviews = replaceReviewLatestRun(p.reviews, reviewState.PRURL, reviewState.TargetSHA, run)
	}
	return TriggerResult{}, false, nil
}

// reuseWhenNothingDue ends the trigger when no run was created or restarted.
func (p *triggerPass) reuseWhenNothingDue() (TriggerResult, bool, error) {
	if len(p.created) != 0 || len(p.restarted) != 0 {
		return TriggerResult{}, false, nil
	}
	if p.modeChanging && p.mode == "" {
		if err := p.e.stopReviewChatWork(p.ctx, p.reviewRow); err != nil {
			return TriggerResult{}, false, err
		}
		if err := p.e.setReviewerInterfaceMode(p.ctx, p.reviewRow.ID, p.selectedMode, p.now); err != nil {
			return TriggerResult{}, false, err
		}
		if err := p.e.store.ClearReviewerHandleByHarness(p.ctx, p.workerID, p.harness); err != nil {
			return TriggerResult{}, false, err
		}
		if _, err := p.e.store.UpdateReviewAgentSessionID(p.ctx, p.reviewRow.ID, ""); err != nil {
			return TriggerResult{}, false, err
		}
		p.reviewRow.InterfaceMode = p.selectedMode
		p.reviewRow.ReviewerHandleID = ""
		p.reviewRow.AgentSessionID = ""
		p.reviewRow.ProviderConversationID = ""
		p.reviewRow.ControllerGeneration = ""
	}
	if p.opts.RejectReviewedHead || (p.opts.Rerun && p.source == domain.ReviewTriggerAgent) {
		return TriggerResult{}, false, nothingToReviewError(p.reviews, p.runs, p.harness, p.opts.Rerun)
	}
	return TriggerResult{Run: firstReusableRun(p.reviews), ReviewerHandleID: legacyReviewerHandle(p.reviewRow), Created: false, Reviews: p.reviews, Runs: p.runs, ReviewerSurface: reviewerSurface(p.reviewRow)}, true, nil
}

// result is the outcome of a trigger that started a pass.
func (p *triggerPass) result() TriggerResult {
	for i := range p.created {
		p.created[i].ReviewID = p.reviewRow.ID
	}
	triggerRuns := append([]domain.ReviewRun{}, p.created...)
	triggerRuns = append(triggerRuns, p.runs...)
	createdFlag := len(p.created) > 0 || len(p.restarted) > 0
	return TriggerResult{Run: p.launchRun, ReviewerHandleID: legacyReviewerHandle(p.reviewRow), Created: createdFlag, Reviews: p.reviews, Runs: triggerRuns, CreatedRuns: p.created, ReviewerSurface: reviewerSurface(p.reviewRow)}
}
