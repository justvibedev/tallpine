package review

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
)

// triggerWithOptions is the single instrumented trigger path. Every entry
// point routes through it so an automatic pass is never invisible: before
// this, only the manual Trigger emitted, which made auto-review
// indistinguishable from manual review in every downstream funnel even though
// the two answer completely different product questions.
func (s *Service) triggerWithOptions(
	ctx context.Context,
	workerID domain.SessionID,
	opts reviewcore.TriggerOptions,
) (reviewcore.TriggerResult, error) {
	return s.runTrigger(ctx, workerID, opts, true)
}

// runTrigger is triggerWithOptions; wake is false for a sweep's own attempt,
// which must not wake the sweep that is running it.
func (s *Service) runTrigger(
	ctx context.Context,
	workerID domain.SessionID,
	opts reviewcore.TriggerOptions,
	wake bool,
) (reviewcore.TriggerResult, error) {
	harness, config := opts.Harness, opts.Config
	if err := config.Validate(); err != nil {
		err = fmt.Errorf("%w: reviewer config: %w", ErrInvalid, err)
		return reviewcore.TriggerResult{}, err
	}
	usesCodex := s.codexReviewUsesCodex(ctx, workerID, harness)
	var release func()
	if usesCodex && s.codexOperationGate != nil {
		var err error
		release, err = s.codexOperationGate.AcquireSharedWait(ctx)
		if err != nil {
			return reviewcore.TriggerResult{}, err
		}
		defer release()
	}
	result, err := s.engineTrigger(ctx, workerID, opts)
	// A trigger can fail passes: a stale or overdue reviewer, or a launch error.
	if wake {
		s.wakeSweep()
	}
	if err != nil {
		return result, err
	}
	// A created run or a restarted pass is an actual review. Reused and skipped
	// automatic attempts can recur on every sweep and are not new reviews.
	createdOrRestarted := result.Created || len(result.CreatedRuns) > 0
	if !createdOrRestarted {
		return result, nil
	}
	s.noticeRunsStarted(ctx, workerID, result)
	return result, nil
}
