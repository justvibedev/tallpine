package review

import (
	stdctx "context"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// failRuns marks every run this pass created as failed and returns err (or the
// store error that stopped the marking).
func (p *triggerPass) failRuns(err error) error {
	for _, run := range p.created {
		if _, updateErr := p.e.store.UpdateReviewRunResult(p.ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, err.Error(), "", run.AutoInjectReview); updateErr != nil {
			return updateErr
		}
	}
	return err
}

// destroyReplacement tears down a reviewer handle this pass started when a
// later step fails and nothing needs the old surface restored.
func (p *triggerPass) destroyReplacement() {
	if p.handleID != "" {
		_ = p.e.launcher.Destroy(p.ctx, p.handleID)
	}
}

// restoresSurface reports whether a failure must put the previous reviewer
// surface back rather than just failing the new runs.
func (p *triggerPass) restoresSurface() bool {
	return p.previousReview.IsArchived || p.modeChanging
}

// rollbackReplacement undoes a half-started replacement reviewer and fails the
// runs this pass created.
func (p *triggerPass) rollbackReplacement(cause error) error {
	e, ctx := p.e, p.ctx
	rollbackNow := e.clock()
	if p.handleID != "" {
		if cleanupErr := e.launcher.Destroy(ctx, p.handleID); cleanupErr != nil {
			cause = errors.Join(cause, fmt.Errorf("cleanup replacement reviewer: %w", cleanupErr))
		}
	}
	if p.hasReview {
		previousReview := p.previousReview
		if p.previousStopped {
			previousReview.ControllerGeneration = ""
			previousReview.ReviewerActivityState = domain.ActivityExited
		}
		rollbackCtx, cancel := stdctx.WithTimeout(stdctx.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if previousReview.IsArchived || p.modeChanging || (p.hasConfigOverride && p.selectedMode == domain.ReviewerInterfaceChat) {
			if ok, rollbackErr := e.store.RestoreReviewLaunchState(rollbackCtx, previousReview, rollbackNow); rollbackErr != nil {
				cause = errors.Join(cause, fmt.Errorf("restore previous reviewer surface: %w", rollbackErr))
			} else if !ok {
				cause = errors.Join(cause, fmt.Errorf("%w: reviewer %q", ErrNotFound, previousReview.ID))
			}
		} else if _, rollbackErr := e.upsertReview(rollbackCtx, p.worker, p.harness, p.previousHandleID, p.previousAgentSessionID, p.previousLaunchID, "", p.now); rollbackErr != nil {
			cause = errors.Join(cause, fmt.Errorf("rollback review row: %w", rollbackErr))
		}
	}
	if p.previousStopped {
		for _, run := range p.restarted {
			if _, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, cause.Error(), "", run.AutoInjectReview); err != nil {
				cause = errors.Join(cause, err)
			}
		}
	}
	return p.failRuns(cause)
}

// launchReviewer starts (or notifies) the reviewer for the runs this pass
// recorded and settles the reviewer row.
func (p *triggerPass) launchReviewer() (TriggerResult, bool, error) {
	if err := p.prepareLaunch(); err != nil {
		return TriggerResult{}, false, err
	}
	if p.handleID == "" {
		if err := p.spawnReviewer(); err != nil {
			return TriggerResult{}, false, err
		}
	} else if err := p.notifyReviewer(); err != nil {
		return TriggerResult{}, false, err
	}
	if err := p.settleLaunch(); err != nil {
		return TriggerResult{}, false, err
	}
	return TriggerResult{}, false, nil
}

// prepareLaunch snapshots the previous reviewer state, builds the launch queue
// and reuses the live pane when it is still usable.
func (p *triggerPass) prepareLaunch() error {
	if p.hasReview {
		if err := p.markReviewerActive(); err != nil {
			return p.failRuns(err)
		}
	}
	p.previousHandleID = p.reviewRow.ReviewerHandleID
	p.previousAgentSessionID = p.reviewRow.AgentSessionID
	p.previousLaunchID = p.reviewRow.ReviewerLaunchID
	p.deferInitialMessage = p.modeChanging && p.previousHandleID != ""

	p.queueRuns = append([]domain.ReviewRun{}, p.restarted...)
	p.queueRuns = append(p.queueRuns, p.created...)
	p.queue = reviewQueue(p.queueRuns)
	for i := range p.queue {
		if IsLocalTarget(p.queue[i].PRURL) {
			for _, pr := range p.prs {
				if pr.URL == p.queue[i].PRURL {
					p.queue[i].BaseRef = pr.BaseSHA
				}
			}
		}
	}
	p.launchRun = p.queueRuns[0]
	p.launchAgentSessionID = p.reviewRow.AgentSessionID
	if p.hasConfigOverride || p.selectedMode == domain.ReviewerInterfaceChat {
		p.launchAgentSessionID = ""
	}
	p.persistedAgentSessionID = p.launchAgentSessionID
	if !p.hasConfigOverride && p.reviewRow.ReviewerHandleID != "" && reviewerPaneReusable(p.reviewRow, p.hadRunningReviewer) {
		alive, err := p.e.launcher.Alive(p.ctx, p.reviewRow.ReviewerHandleID, p.reviewRow.ReviewerLaunchID)
		if err != nil {
			return p.failRuns(err)
		}
		if alive {
			p.handleID = p.reviewRow.ReviewerHandleID
			// The account is fixed at process start: after the worker's
			// account changes, replace the reviewer instead of reusing it, unless it
			// is mid-review: killing it would strand its running run (the next pass
			// replaces it once that run ends).
			if s, ok := p.e.launcher.(interface {
				AccountStale(stdctx.Context, string, domain.SessionID, domain.ReviewerHarness) bool
			}); ok && !p.hadRunningReviewer && s.AccountStale(p.ctx, p.handleID, p.worker.ID, p.harness) {
				if err := p.e.launcher.Destroy(p.ctx, p.handleID); err != nil {
					return p.failRuns(err)
				}
				p.handleID = ""
			}
		}
	}
	return nil
}

// spawnReviewer starts a fresh reviewer process on the stable terminal handle.
func (p *triggerPass) spawnReviewer() error {
	e, ctx, worker, harness := p.e, p.ctx, p.worker, p.harness
	// Each pass gets a fresh reviewer process on the same stable terminal
	// handle when there is no resumable live agent session to notify.
	releaseHarness, err := e.beginHarnessUse(harness)
	if err != nil {
		return p.failRuns(err)
	}
	p.releaseHarness = releaseHarness
	if err := e.launcher.Preflight(ctx, worker.ID, harness, worker.Metadata.WorkspacePath, p.selectedMode); err != nil {
		return p.failRuns(fmt.Errorf("reviewer preflight: %w", err))
	}
	// A review owns one Chat controller. Stop it while its durable fence
	// still belongs to it before a config replacement claims a new epoch.
	if p.hasConfigOverride && !p.modeChanging && p.selectedMode == domain.ReviewerInterfaceChat {
		if err := e.stopReviewChatWork(ctx, p.reviewRow); err != nil {
			return p.failRuns(err)
		}
		p.previousStopped = true
	}
	launchID := e.newID()
	// The new launch starts active and never inherits the previous launch's
	// state: destroying the old pane (the account-follow relaunch in
	// prepareLaunch) lets its supervisor post "exited" while the row still
	// names its launch, and reconcile trusts "exited" without probing (B364).
	p.reviewRow, err = e.upsertReview(ctx, worker, harness, p.reviewRow.ReviewerHandleID, p.launchAgentSessionID, launchID, domain.ActivityActive, p.now)
	if err != nil {
		return p.rollbackReplacement(err)
	}
	if p.reviewRow.IsArchived {
		// Chat must be active before its controller can claim ownership. Only
		// reopen once launch work is due; rollback restores the archive on failure.
		p.reviewRow.IsArchived = false
		p.reviewRow.ReviewerActivityState = domain.ActivityActive
		if err := e.store.UpsertReview(ctx, p.reviewRow); err != nil {
			return p.rollbackReplacement(err)
		}
	}
	if err := e.setReviewerInterfaceMode(ctx, p.reviewRow.ID, p.selectedMode, p.now); err != nil {
		return p.rollbackReplacement(err)
	}
	projectEnv, err := e.projectEnv(ctx, worker.ProjectID, harness)
	if err != nil {
		return p.rollbackReplacement(err)
	}
	launchSpec := e.reviewLaunchSpec(ctx, worker, harness, p.config, p.launchRun, p.queue, p.launchAgentSessionID, launchID)
	launchSpec.ProjectEnv = projectEnv
	launchSpec.InterfaceMode = p.selectedMode
	launchSpec.DeferInitialMessage = p.deferInitialMessage
	launchSpec.PreviousHandleID = p.previousHandleID
	if p.selectedMode == domain.ReviewerInterfaceChat && !p.hasConfigOverride {
		launchSpec.ProviderConversationID = p.reviewRow.ProviderConversationID
	}
	launch, err := e.launcher.Spawn(ctx, launchSpec)
	if err != nil {
		return p.rollbackReplacement(fmt.Errorf("launch reviewer: %w", err))
	}
	p.handleID = launch.HandleID
	if launch.LaunchID != "" {
		p.reviewRow.ReviewerLaunchID = launch.LaunchID
	}
	if launch.AgentSessionID != "" {
		p.persistedAgentSessionID = launch.AgentSessionID
	}
	return nil
}

// notifyReviewer hands the new queue to the live reviewer pane.
func (p *triggerPass) notifyReviewer() error {
	launchSpec := p.e.reviewLaunchSpec(p.ctx, p.worker, p.harness, p.config, p.launchRun, p.queue, p.reviewRow.AgentSessionID, p.reviewRow.ReviewerLaunchID)
	launchSpec.InterfaceMode = p.selectedMode
	if err := p.e.launcher.Notify(p.ctx, p.handleID, launchSpec); err != nil {
		return p.failRuns(fmt.Errorf("notify reviewer: %w", err))
	}
	return nil
}

// failSettle handles a failure after the reviewer started: a surface-changing
// pass restores the previous surface, otherwise the new pane is destroyed.
// destroyOnly keeps the original error and skips failing the runs (the final
// upsert failure path returned its error as is).
func (p *triggerPass) failSettle(err error, destroyOnly bool) error {
	if p.restoresSurface() {
		return p.rollbackReplacement(err)
	}
	p.destroyReplacement()
	if destroyOnly {
		return err
	}
	return p.failRuns(err)
}

// settleLaunch supersedes stale runs, records the final reviewer row and
// retires the previous pane when the configuration replaced it.
func (p *triggerPass) settleLaunch() error {
	e, ctx := p.e, p.ctx
	for _, stale := range p.pendingSupersedes {
		if _, err := e.store.SupersedeStaleRunningReviewRuns(ctx, p.workerID, stale.prURL, stale.targetSHA, reviewRunSupersededReason); err != nil {
			return p.failSettle(err, false)
		}
	}
	if p.hasConfigOverride && p.persistedAgentSessionID == "" && p.reviewRow.ID != "" {
		if _, err := e.store.UpdateReviewAgentSessionID(ctx, p.reviewRow.ID, ""); err != nil {
			return p.failSettle(err, false)
		}
	}
	e.markLaunched(p.handleID)
	var err error
	p.reviewRow, err = e.upsertReview(ctx, p.worker, p.harness, p.handleID, p.persistedAgentSessionID, p.reviewRow.ReviewerLaunchID, "", p.now)
	if err != nil {
		return p.failSettle(err, true)
	}
	if p.hasConfigOverride && p.previousHandleID != "" && p.previousHandleID != p.handleID {
		return p.retirePreviousReviewer()
	}
	return nil
}

// retirePreviousReviewer destroys the pane the new configuration replaced and,
// for a deferred first message, notifies the replacement.
func (p *triggerPass) retirePreviousReviewer() error {
	e, ctx := p.e, p.ctx
	if err := e.launcher.Destroy(ctx, p.previousHandleID); err != nil {
		if p.modeChanging {
			return p.rollbackReplacement(fmt.Errorf("destroy previous reviewer: %w", err))
		}
		if _, rollbackErr := e.upsertReview(ctx, p.worker, p.harness, p.previousHandleID, p.previousAgentSessionID, "", "", p.now); rollbackErr != nil {
			return p.failRuns(fmt.Errorf("destroy previous reviewer: %w; rollback review row: %w", err, rollbackErr))
		}
		if p.handleID != "" {
			if destroyNewErr := e.launcher.Destroy(ctx, p.handleID); destroyNewErr != nil {
				return p.failRuns(fmt.Errorf("destroy previous reviewer: %w; cleanup replacement reviewer: %w", err, destroyNewErr))
			}
		}
		return p.failRuns(fmt.Errorf("destroy previous reviewer: %w", err))
	}
	p.previousStopped = true
	if p.deferInitialMessage {
		launchSpec := e.reviewLaunchSpec(ctx, p.worker, p.harness, p.config, p.launchRun, p.queue, p.persistedAgentSessionID, p.reviewRow.ReviewerLaunchID)
		launchSpec.InterfaceMode = p.selectedMode
		if err := e.launcher.Notify(ctx, p.handleID, launchSpec); err != nil {
			return p.rollbackReplacement(fmt.Errorf("notify replacement reviewer: %w", err))
		}
	}
	return nil
}
