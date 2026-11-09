package review

import (
	stdctx "context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// CountsTowardAutoRetryLimit reports whether a pass on a head used up one of
// the automatic pass's retries without reviewing it: a failed auto pass, or a
// pass cancelled because its reviewer vanished. The engine and the auto-review
// coordinator share this rule so the two gates cannot drift apart.
func CountsTowardAutoRetryLimit(run domain.ReviewRun) bool {
	return (run.Status == domain.ReviewRunFailed && run.TriggerSource == domain.ReviewTriggerAuto) || ReviewerGoneCancel(run)
}

// TransientAutoSkip reports whether an automatic trigger was skipped only
// because the worker is busy right now, so the same pass can start later.
func TransientAutoSkip(reason string) bool {
	return reason == "not_idle" || reason == "idle_threshold_not_met"
}

func autoReviewHeadBlocked(runs []domain.ReviewRun, prURL, targetSHA string, harness domain.ReviewerHarness) bool {
	retries := 0
	for _, run := range runs {
		if run.PRURL != prURL || run.TargetSHA != targetSHA || (run.Harness != harness && run.Harness != "") {
			continue
		}
		if CountsTowardAutoRetryLimit(run) {
			retries++
			if retries >= autoReviewFailedRetryLimit {
				return true
			}
			continue
		}
		if run.Status == domain.ReviewRunRunning || run.Status == domain.ReviewRunCancelled || run.Verdict == domain.VerdictApproved || run.Verdict == domain.VerdictChangesRequested {
			return true
		}
	}
	return false
}

func reviewRunsForHarness(runs []domain.ReviewRun, harness domain.ReviewerHarness) []domain.ReviewRun {
	out := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.Harness == harness {
			out = append(out, run)
		}
	}
	return out
}

func reviewRunsForReview(runs []domain.ReviewRun, reviewID string) []domain.ReviewRun {
	out := make([]domain.ReviewRun, 0, len(runs))
	for _, run := range runs {
		if run.ReviewID == reviewID {
			out = append(out, run)
		}
	}
	return out
}

// cancelStaleRunningRuns fails this reviewer's running passes when its pane is
// gone. It is scoped to one reviewer so a dead pane cannot cancel another
// reviewer that is still working on the same worker.
func (e *Engine) cancelStaleRunningRuns(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness, reviewRow domain.Review, hasReview bool, runs []domain.ReviewRun) (bool, error) {
	hasRunning := false
	for _, run := range runs {
		if run.SessionID == workerID && run.Status == domain.ReviewRunRunning && run.Verdict == domain.VerdictNone && (run.Harness == harness || run.Harness == "") {
			hasRunning = true
			break
		}
	}
	if !hasRunning {
		return false, nil
	}
	// A reviewer the session-end hook marked exited is gone without a probe.
	if hasReview && reviewRow.ReviewerHandleID != "" && reviewRow.ReviewerActivityState != domain.ActivityExited {
		alive, err := e.launcher.Alive(ctx, reviewRow.ReviewerHandleID, reviewRow.ReviewerLaunchID)
		if err != nil {
			return false, err
		}
		if alive {
			return false, nil
		}
	}
	for _, h := range []domain.ReviewerHarness{harness, ""} {
		// Legacy rows predate per-harness runs and belong to whichever reviewer
		// is current, so they go with it.
		if _, err := e.store.CancelRunningReviewRunsBySessionAndHarness(ctx, workerID, h, reviewerTerminalUnavailableReason); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (e *Engine) reviewLaunchSpec(
	ctx stdctx.Context,
	worker domain.SessionRecord,
	harness domain.ReviewerHarness,
	config domain.AgentConfig,
	run domain.ReviewRun,
	queue []ports.ReviewTask,
	agentSessionID string,
	launchID string,
) LaunchSpec {
	decisions, focus := e.planFor(ctx, worker.ID)
	return LaunchSpec{
		RunID:           run.ID,
		BatchID:         run.BatchID,
		ReviewSessionID: run.ReviewID,
		LaunchID:        launchID,
		WorkerID:        worker.ID,
		ProjectID:       worker.ProjectID,
		Harness:         harness,
		AgentConfig:     e.withReviewerLaunch(ctx, worker.ProjectID, config),
		WorkspacePath:   worker.Metadata.WorkspacePath,
		AgentSessionID:  agentSessionID,
		PreviousRuns:    nil,
		PRURL:           run.PRURL,
		TargetSHA:       run.TargetSHA,
		ReviewQueue:     queue,
		TaskContext:     taskContext(worker.Metadata.Prompt),
		PlanDecisions:   decisions,
		PlanFocus:       focus,
	}
}

func reviewQueue(runs []domain.ReviewRun) []ports.ReviewTask {
	queue := make([]ports.ReviewTask, 0, len(runs))
	for _, run := range runs {
		queue = append(queue, ports.ReviewTask{
			RunID:     run.ID,
			PRURL:     run.PRURL,
			TargetSHA: run.TargetSHA,
		})
	}
	return queue
}

// secondOpinionWanted reports whether an explicit manual override makes an
// otherwise up-to-date PR worth running again. A config-only override (for
// example, picking a model under the same harness) always requests another
// pass. A harness-only override only does so when it actually changes the
// reviewer whose pass is currently authoritative; re-picking the same harness
// must still reuse. That remains true even while another harness is already
// running: a different reviewer is a second opinion, not a restart. Legacy
// runs may have an empty harness, which means "the same resolved harness as
// today" for this comparison.
func secondOpinionWanted(state PRReviewState, hasHarnessOverride, hasConfigOverride bool, harness domain.ReviewerHarness) bool {
	if state.Status == ReviewStateIneligible {
		return false
	}
	if state.LatestRun == nil {
		return false
	}
	if hasConfigOverride {
		return true
	}
	if !hasHarnessOverride {
		return false
	}
	latestHarness := state.LatestRun.Harness
	if latestHarness == "" {
		latestHarness = harness
	}
	return latestHarness != harness
}

func replaceReviewLatestRun(reviews []PRReviewState, prURL, targetSHA string, run domain.ReviewRun) []PRReviewState {
	for i := range reviews {
		if reviews[i].PRURL != prURL || reviews[i].TargetSHA != targetSHA {
			continue
		}
		reviews[i].LatestRun = &run
		known := false
		for _, existing := range reviews[i].CurrentHeadRuns {
			known = known || existing.ID == run.ID
		}
		if !known {
			reviews[i].CurrentHeadRuns = append([]domain.ReviewRun{run}, reviews[i].CurrentHeadRuns...)
		}
		if run.Status == domain.ReviewRunRunning {
			reviews[i].Status = ReviewStateRunning
		}
		break
	}
	return reviews
}

func firstReusableRun(reviews []PRReviewState) domain.ReviewRun {
	// Legacy compatibility only: in the multi-PR model the authoritative state
	// is Reviews. When no run is created, this field is just a best-effort
	// non-empty run for older clients.
	for _, review := range reviews {
		if review.LatestRun != nil {
			return *review.LatestRun
		}
	}
	return domain.ReviewRun{}
}
