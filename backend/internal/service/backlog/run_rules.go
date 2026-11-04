package backlog

import (
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func transitionError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", domain.ErrBacklogRunTransition, fmt.Sprintf(format, args...))
}

// applyItemUpdate moves one planned item to a new status under the run rules:
// nothing starts unless the run is running, at most MaxBacklogRunWorkers items
// are active per project, a landed item stays landed, needs_you carries its
// question, held carries its reason, and the second failure holds the item.
// active is how many items already hold a slot in the project (this one included).
func applyItemUpdate(run domain.BacklogRun, it *domain.BacklogRunItem, up RunItemUpdate, active int) error {
	if !up.Status.Valid() {
		return fmt.Errorf("%w: unknown item status %q", domain.ErrBacklogRunInvalid, up.Status)
	}
	if !run.Status.Open() {
		return transitionError("run %s is done", run.Ref())
	}
	if it.Status == domain.BacklogRunItemLanded && up.Status != domain.BacklogRunItemLanded {
		return transitionError("%s already landed", it.ItemRef())
	}
	if up.Status == domain.BacklogRunItemActive && it.Status != domain.BacklogRunItemActive {
		if run.Status != domain.BacklogRunRunning {
			return transitionError("run %s is %s, nothing new starts", run.Ref(), run.Status)
		}
		if active >= domain.MaxBacklogRunWorkers {
			return fmt.Errorf("%w: %d items are already active", domain.ErrBacklogRunWorkerCap, active)
		}
	}
	it.Reason, it.Question = up.Reason, ""
	it.Status = up.Status
	switch up.Status {
	case domain.BacklogRunItemNeedsYou:
		if up.Question == "" {
			return fmt.Errorf("%w: needs_you requires a question", domain.ErrBacklogRunInvalid)
		}
		it.Question = up.Question
	case domain.BacklogRunItemHeld:
		if up.Reason == "" {
			return fmt.Errorf("%w: held requires a reason", domain.ErrBacklogRunInvalid)
		}
	case domain.BacklogRunItemFailed:
		it.Attempts++
		if it.Attempts >= domain.MaxBacklogRunAttempts {
			it.Status = domain.BacklogRunItemHeld
			if it.Reason == "" {
				it.Reason = "failed twice"
			}
		}
	}
	return nil
}

// settle ends the run when nothing is left to do: every planned item settled,
// or a stop with nothing in flight. Only a run with a plan can settle.
func settle(run *domain.BacklogRun, items []domain.BacklogRunItem, now time.Time) {
	if !run.Status.Open() || len(items) == 0 {
		return
	}
	inFlight, unsettled := false, false
	for _, it := range items {
		inFlight = inFlight || it.Status.InFlight()
		// An item waiting for its plan is not settled: the plan is the next step.
		unsettled = unsettled || !it.Status.Settled() || it.Reason == domain.BacklogRunReasonPlanPending
	}
	if (run.Status == domain.BacklogRunStopping && !inFlight) || !unsettled {
		finishRun(run, now)
	}
}

func finishRun(run *domain.BacklogRun, now time.Time) {
	run.Status = domain.BacklogRunDone
	run.PauseReason, run.ResumeAt = "", nil
	run.EndedAt = &now
}

// pauseRun parks a running run, usually at the Claude usage limit.
func pauseRun(run *domain.BacklogRun, in RunPauseInput) error {
	if run.Status != domain.BacklogRunRunning {
		return transitionError("run %s is %s, only a running run pauses", run.Ref(), run.Status)
	}
	run.Status, run.PauseReason, run.ResumeAt = domain.BacklogRunPaused, in.Reason, in.ResumeAt
	return nil
}

func resumeRun(run *domain.BacklogRun) error {
	if run.Status != domain.BacklogRunPaused {
		return transitionError("run %s is %s, only a paused run resumes", run.Ref(), run.Status)
	}
	run.Status, run.PauseReason, run.ResumeAt = domain.BacklogRunRunning, "", nil
	return nil
}

// stopRun asks the run to finish what is in flight and start nothing new.
func stopRun(run *domain.BacklogRun) error {
	switch run.Status {
	case domain.BacklogRunPlanning, domain.BacklogRunRunning, domain.BacklogRunPaused:
		run.Status, run.PauseReason, run.ResumeAt = domain.BacklogRunStopping, "", nil
		return nil
	}
	return transitionError("run %s is already %s", run.Ref(), run.Status)
}

// closeRun records the orchestrator's summary and ends the run. It refuses
// while work is still in flight; a run that already settled only takes the summary.
func closeRun(run *domain.BacklogRun, items []domain.BacklogRunItem, summary string, now time.Time) error {
	for _, it := range items {
		if it.Status.InFlight() {
			return transitionError("%s is still %s", it.ItemRef(), it.Status)
		}
	}
	run.Summary = summary
	if run.Status.Open() {
		finishRun(run, now)
	}
	return nil
}
