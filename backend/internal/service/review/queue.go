package review

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

const (
	// queuedTriggerMaxAge and queuedTriggerMaxAttempts bound how long a pass
	// that keeps failing to start stays queued.
	queuedTriggerMaxAge      = 24 * time.Hour
	queuedTriggerMaxAttempts = 5
	queuedTriggerBackoff     = time.Minute
	queuedTriggerMaxBackoff  = 30 * time.Minute
)

// queuedAttemptDelay is the wait after the attempts-th failed start: 2, 4, 8...
// minutes, capped.
func queuedAttemptDelay(attempts int) time.Duration {
	d := queuedTriggerBackoff << min(attempts, 10)
	return min(d, queuedTriggerMaxBackoff)
}

// drainQueuedTriggers starts each queued pass whose reviewer is now free. It
// runs on every sweep, including the first after a daemon start, so a queue
// written before a restart resumes. An entry leaves the queue once its pass
// started, once another pass already covers the head, or when it can never
// start (the worker or PR is gone, it failed too often, or it is too old); the
// worker hears about the last group. A busy reviewer or a backoff keeps it.
func (s *Service) drainQueuedTriggers(ctx context.Context) {
	queued, err := s.engine.QueuedTriggers(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "review sweep could not read the trigger queue", "err", err)
		return
	}
	status, gated := s.engine.MachineStatus(ctx)
	closed := gated && status.Busy
	if closed {
		// A hold is not a failed attempt: nothing is counted or delayed, the
		// entries just wait for the machine. Only their age still runs out.
		s.dropExpiredQueued(ctx, queued, "the machine stayed busy for "+queuedTriggerMaxAge.String())
	}
	oneStartPerSweep := gated && (status.MaxReviewers > 0 || status.LoadPercent > 0)
	attempted := map[domain.QueuedReviewTrigger]bool{}
	for _, q := range queued {
		if ctx.Err() != nil {
			return
		}
		// The owner's own review (manual) never waits for the machine.
		manual := q.TriggerSource == domain.ReviewTriggerManual
		if closed && !manual {
			continue
		}
		key := domain.QueuedReviewTrigger{SessionID: q.SessionID, Harness: q.Harness}
		if attempted[key] || s.clock().Before(q.NextAttemptAt) {
			continue
		}
		running, err := s.engine.ReviewerBusy(ctx, q.SessionID, q.Harness)
		if err != nil {
			slog.Default().WarnContext(ctx, "review sweep could not check the reviewer for a queued pass", "session", q.SessionID, "err", err)
			continue
		}
		if running {
			attempted[key] = true
			continue
		}
		attempted[key] = true // one pass per reviewer per sweep; the next waits for it
		held, started := s.startQueuedTrigger(ctx, q)
		if held {
			attempted[key] = false // held by a busy worker: a newer entry may still go
		}
		if started && oneStartPerSweep && !manual {
			return // the next start waits for the load this one is about to add
		}
	}
}

// dropExpiredQueued drops the entries older than the maximum age.
func (s *Service) dropExpiredQueued(ctx context.Context, queued []domain.QueuedReviewTrigger, reason string) {
	for _, q := range queued {
		if s.clock().Sub(q.CreatedAt) > queuedTriggerMaxAge {
			s.dropQueuedTrigger(ctx, q, reason)
		}
	}
}

// startQueuedTrigger tries one entry and reports whether it was only held back
// because the worker or the machine is busy right now, and whether it started a
// new pass.
func (s *Service) startQueuedTrigger(ctx context.Context, q domain.QueuedReviewTrigger) (held, started bool) {
	triggerCtx, cancel := context.WithTimeout(ctx, sweepReconcileTimeout)
	defer cancel()
	source := q.TriggerSource
	if source == "" {
		source = domain.ReviewTriggerAgent
	}
	opts := reviewcore.TriggerOptions{
		Harness: q.Harness, Config: domain.AgentConfig{Model: q.Model, Effort: q.Effort}, InterfaceMode: q.InterfaceMode,
		Source: source, PRURL: q.PRURL, Focus: q.Focus, FocusAuthor: q.FocusAuthor,
	}
	if source != domain.ReviewTriggerAuto {
		// A requested pass reviews the head again; an automatic one re-checks
		// the auto policy (and is dropped quietly when it no longer applies).
		opts.Rerun, opts.QueuedSince = true, q.CreatedAt
	}
	result, err := s.runTrigger(triggerCtx, q.SessionID, opts, false)
	started = err == nil && (result.Created || len(result.CreatedRuns) > 0)
	switch {
	case err == nil && (result.Queued || s.startedNothingWhileBusy(ctx, q, result)):
		return held, false // the reviewer got busy again; this entry is still the only copy
	case err == nil && reviewcore.TransientAutoSkip(result.SkipReason):
		// The worker or the machine is busy right now: the same pass can start
		// on a later sweep.
		held = true
		if s.clock().Sub(q.CreatedAt) > queuedTriggerMaxAge {
			s.dropQueuedTrigger(ctx, q, "it stayed held back for "+queuedTriggerMaxAge.String())
		}
		return held, false
	case errors.Is(err, reviewcore.ErrHarnessInstallActive):
		return held, false // not a failure of the pass: try again next sweep
	case err == nil, errors.Is(err, reviewcore.ErrReviewAlreadyRunning):
		// Started, or another pass already covers the head: nothing left to do.
	case errors.Is(err, reviewcore.ErrInvalid), errors.Is(err, reviewcore.ErrNotFound):
		s.dropQueuedTrigger(ctx, q, err.Error())
		return held, false
	default:
		q.Attempts++
		if q.Attempts >= queuedTriggerMaxAttempts {
			s.dropQueuedTrigger(ctx, q, "it failed to start "+fmt.Sprint(q.Attempts)+" times: "+err.Error())
			return held, false
		}
		if s.clock().Sub(q.CreatedAt) > queuedTriggerMaxAge {
			s.dropQueuedTrigger(ctx, q, "it waited more than "+queuedTriggerMaxAge.String()+" and failed to start: "+err.Error())
			return held, false
		}
		slog.Default().WarnContext(ctx, "review sweep could not start a queued pass; keeping it", "session", q.SessionID, "pr", q.PRURL, "attempts", q.Attempts, "err", err)
		if err := s.engine.RecordQueuedAttempt(ctx, q, s.clock().Add(queuedAttemptDelay(q.Attempts))); err != nil {
			slog.Default().WarnContext(ctx, "review sweep could not record a queued pass attempt", "session", q.SessionID, "err", err)
		}
		return held, false
	}
	if err := s.engine.DropQueuedTrigger(ctx, q); err != nil {
		slog.Default().WarnContext(ctx, "review sweep could not clear a queued pass", "session", q.SessionID, "err", err)
	}
	return held, started
}

// dropQueuedTrigger removes a pass that can never start and tells its worker,
// so a request does not vanish silently.
func (s *Service) dropQueuedTrigger(ctx context.Context, q domain.QueuedReviewTrigger, reason string) {
	slog.Default().WarnContext(ctx, "review sweep dropped a queued pass that cannot start", "session", q.SessionID, "pr", q.PRURL, "reason", reason)
	if err := s.engine.DropQueuedTrigger(ctx, q); err != nil {
		slog.Default().WarnContext(ctx, "review sweep could not clear a queued pass", "session", q.SessionID, "err", err)
		return
	}
	notice := s.failureNotice()
	if notice == nil || notice.nudger == nil {
		return
	}
	message := fmt.Sprintf("The queued %s review of %s never started and was dropped: %s. Ask for it again with `tallpine review trigger` when you want it.", q.Harness, q.PRURL, reason)
	if _, err := notice.nudger.Nudge(ctx, q.SessionID, message); err != nil {
		slog.Default().WarnContext(ctx, "review sweep could not tell the worker a queued pass was dropped", "session", q.SessionID, "err", err)
	}
}

// startedNothingWhileBusy reports whether a trigger that created nothing did so
// because the reviewer took another pass in the meantime.
func (s *Service) startedNothingWhileBusy(ctx context.Context, q domain.QueuedReviewTrigger, result reviewcore.TriggerResult) bool {
	if result.Created || len(result.CreatedRuns) > 0 || result.SkipReason != "" {
		return false
	}
	busy, err := s.engine.ReviewerBusy(ctx, q.SessionID, q.Harness)
	return err == nil && busy
}
