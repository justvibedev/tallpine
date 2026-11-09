package review

import (
	stdctx "context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// TriggerQueue persists review passes waiting for their reviewer. A store that
// does not implement it (a test fake) simply has no queue: busy reruns are
// rejected as before and dead passes are not requeued.
type TriggerQueue interface {
	EnqueueReviewTrigger(ctx stdctx.Context, q domain.QueuedReviewTrigger) error
	ListQueuedReviewTriggers(ctx stdctx.Context) ([]domain.QueuedReviewTrigger, error)
	DeleteQueuedReviewTrigger(ctx stdctx.Context, q domain.QueuedReviewTrigger) error
	RecordQueuedReviewTriggerAttempt(ctx stdctx.Context, q domain.QueuedReviewTrigger, nextAttemptAt time.Time) error
}

func (e *Engine) queue() (TriggerQueue, bool) {
	q, ok := e.store.(TriggerQueue)
	return q, ok
}

// queueBusyRerun parks an agent's rerun when its reviewer is busy on another
// head, instead of rejecting it, so the request survives until the reviewer is
// free (and survives a restart). It reports false when nothing could be queued,
// so the caller keeps the rejection: a pass already running on this very head
// is a true duplicate, not a busy reviewer.
func (p *triggerPass) queueBusyRerun() (TriggerResult, bool, error) {
	queue, ok := p.e.queue()
	if !ok {
		return TriggerResult{}, false, nil
	}
	queued := false
	for _, state := range p.reviews {
		if state.Status == ReviewStateIneligible || headRunningForHarness(p.runs, state.PRURL, state.TargetSHA, p.harness) {
			continue
		}
		err := queue.EnqueueReviewTrigger(p.ctx, domain.QueuedReviewTrigger{SessionID: p.workerID, PRURL: state.PRURL, Harness: p.harness, CreatedAt: p.e.clock(), Model: p.opts.Config.Model, Effort: p.opts.Config.Effort, InterfaceMode: p.opts.InterfaceMode, TriggerSource: p.source})
		if err != nil {
			return TriggerResult{}, false, err
		}
		queued = true
	}
	if !queued {
		return TriggerResult{}, false, nil
	}
	return TriggerResult{
		Queued:           true,
		ReviewerHandleID: legacyReviewerHandle(p.reviewRow),
		Reviews:          p.reviews,
		Runs:             p.runs,
		ReviewerSurface:  reviewerSurface(p.reviewRow),
	}, true, nil
}

// QueuedTriggers lists the passes waiting for a reviewer, oldest first.
func (e *Engine) QueuedTriggers(ctx stdctx.Context) ([]domain.QueuedReviewTrigger, error) {
	queue, ok := e.queue()
	if !ok {
		return nil, nil
	}
	return queue.ListQueuedReviewTriggers(ctx)
}

// DropQueuedTrigger removes a waiting pass.
func (e *Engine) DropQueuedTrigger(ctx stdctx.Context, q domain.QueuedReviewTrigger) error {
	queue, ok := e.queue()
	if !ok {
		return nil
	}
	return queue.DeleteQueuedReviewTrigger(ctx, q)
}

// RunningFor returns the passes the reviewer is running for the worker.
func (e *Engine) RunningFor(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness) ([]domain.ReviewRun, error) {
	runs, err := e.store.ListRunningReviewRunsBySession(ctx, workerID)
	if err != nil {
		return nil, err
	}
	var out []domain.ReviewRun
	for _, run := range runs {
		if run.Harness == harness || run.Harness == "" {
			out = append(out, run)
		}
	}
	return out, nil
}

// ReviewerBusy reports whether the reviewer has a running pass for the worker.
func (e *Engine) ReviewerBusy(ctx stdctx.Context, workerID domain.SessionID, harness domain.ReviewerHarness) (bool, error) {
	running, err := e.RunningFor(ctx, workerID, harness)
	return len(running) > 0, err
}

// QueueRerun parks a rerun of prURL for a reviewer that is busy, keeping the
// options of the pass it replaces.
func (e *Engine) QueueRerun(ctx stdctx.Context, q domain.QueuedReviewTrigger) error {
	queue, ok := e.queue()
	if !ok {
		return nil
	}
	q.CreatedAt, q.NextAttemptAt = e.clock(), time.Time{}
	return queue.EnqueueReviewTrigger(ctx, q)
}

// RecordQueuedAttempt counts a failed start of a queued pass and delays the next.
func (e *Engine) RecordQueuedAttempt(ctx stdctx.Context, q domain.QueuedReviewTrigger, next time.Time) error {
	queue, ok := e.queue()
	if !ok {
		return nil
	}
	return queue.RecordQueuedReviewTriggerAttempt(ctx, q, next)
}

// queuedFor lists the queued passes of one worker.
func (e *Engine) queuedFor(ctx stdctx.Context, workerID domain.SessionID) ([]domain.QueuedReviewTrigger, error) {
	all, err := e.QueuedTriggers(ctx)
	if err != nil {
		return nil, err
	}
	var out []domain.QueuedReviewTrigger
	for _, q := range all {
		if q.SessionID == workerID {
			out = append(out, q)
		}
	}
	return out, nil
}

// reviewedSince reports whether the reviewer already has a pass on the PR's
// current head that was started after since and reached the commit, so a queued
// rerun asked for before it has nothing left to do.
func reviewedSince(runs []domain.ReviewRun, state PRReviewState, harness domain.ReviewerHarness, since time.Time) bool {
	for _, run := range runs {
		if run.PRURL != state.PRURL || run.TargetSHA != state.TargetSHA || (run.Harness != harness && run.Harness != "") || !run.CreatedAt.After(since) {
			continue
		}
		if run.Status != domain.ReviewRunFailed && run.Status != domain.ReviewRunCancelled {
			return true
		}
	}
	return false
}
