package review

import (
	stdctx "context"
	"fmt"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

// SkipMachineBusy is the SkipReason of an automatic pass held back because the
// machine is busy; the next sweep tries again.
const SkipMachineBusy = "machine_busy"

// MachineGate tells whether the machine can take another reviewer (B752).
type MachineGate interface {
	// Check reads the machine once.
	Check(ctx stdctx.Context) (machineload.Status, error)
	// Admit is Check that reserves a slot until the returned release runs.
	Admit(ctx stdctx.Context) (func(), machineload.Status, error)
}

// MachineStatus reads the machine-load gate; ok is false when there is no gate
// or it could not read the machine (callers fail open).
func (e *Engine) MachineStatus(ctx stdctx.Context) (machineload.Status, bool) {
	if e.machine == nil {
		return machineload.Status{}, false
	}
	status, err := e.machine.Check(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "review could not read the machine load; not holding", "err", err)
		return machineload.Status{}, false
	}
	return status, true
}

// gateMachine holds a pass that would start a new reviewer while the machine is
// busy. An automatic pass is skipped (the next sweep retries it) and an agent's
// is parked in the trigger queue. The owner's own manual review never waits,
// and a reviewer that already has a running pass adds no new one.
func (p *triggerPass) gateMachine() (TriggerResult, bool, error) {
	if res, held := p.deferToQueuedRequest(); held {
		return res, true, nil
	}
	if p.e.machine == nil || p.source == domain.ReviewTriggerManual || p.hadRunningReviewer {
		return TriggerResult{}, false, nil
	}
	due := p.newPassesDue()
	if len(due) == 0 {
		return TriggerResult{}, false, nil
	}
	release, status, err := p.e.machine.Admit(p.ctx)
	if err != nil {
		slog.Default().WarnContext(p.ctx, "review could not read the machine load; not holding", "session", p.workerID, "err", err)
		return TriggerResult{}, false, nil
	}
	if !status.Busy {
		p.releaseMachine = release
		return TriggerResult{}, false, nil
	}
	reason := status.Reason()
	if p.source == domain.ReviewTriggerAuto {
		return TriggerResult{SkipReason: SkipMachineBusy, HeldReason: reason}, true, nil
	}
	if res, queued, err := p.queueStates(due, reason); queued || err != nil {
		return res, queued, err
	}
	return TriggerResult{}, false, fmt.Errorf("%w: %s; try again shortly", ErrConflict, reason)
}

// newPassesDue lists the PRs this trigger would start a pass for, leaving out a
// restart of a pass that is already running.
func (p *triggerPass) newPassesDue() []PRReviewState {
	var due []PRReviewState
	for _, state := range p.reviews {
		if !prDueForTrigger(state, p.runs, p.harness, p.source, p.opts, p.hasHarnessOverride, p.secondOpinionOverride) {
			continue
		}
		if p.hasConfigOverride && state.LatestRun != nil && state.LatestRun.Status == domain.ReviewRunRunning {
			continue
		}
		due = append(due, state)
	}
	return due
}

// releaseMachineSlot frees the reserved slot. The pass is recorded as running
// by then, so the store's count carries it from here on.
func (p *triggerPass) releaseMachineSlot() {
	if p.releaseMachine != nil {
		p.releaseMachine()
		p.releaseMachine = nil
	}
}

func (p *triggerPass) releaseMachineSlotStep() (TriggerResult, bool, error) {
	p.releaseMachineSlot()
	return TriggerResult{}, false, nil
}

// deferToQueuedRequest keeps an automatic pass off the PRs for which an
// explicit request (agent or owner) to this same reviewer waits in the trigger
// queue. Otherwise the auto sweep starts the pass with the project's default
// reviewer the moment the machine frees up, and the queued request, which
// carries the requested --agent/--model/--effort, finds the head already
// covered and is dropped (B777). Other PRs of the worker are still reviewed;
// the pass is held (a transient skip, retried next sweep) only when nothing
// else is left.
func (p *triggerPass) deferToQueuedRequest() (TriggerResult, bool) {
	if p.source != domain.ReviewTriggerAuto {
		return TriggerResult{}, false
	}
	queued, err := p.e.queuedFor(p.ctx, p.workerID)
	if err != nil {
		return TriggerResult{}, false
	}
	held := map[string]bool{}
	for _, q := range queued {
		if q.TriggerSource != domain.ReviewTriggerAuto && q.Harness == p.harness {
			held[q.PRURL] = true
		}
	}
	if len(held) == 0 {
		return TriggerResult{}, false
	}
	open, hit := false, false
	for i := range p.reviews {
		switch {
		case held[p.reviews[i].PRURL]:
			hit = true
			p.reviews[i].Status = ReviewStateIneligible
		case p.reviews[i].Status != ReviewStateIneligible:
			open = true
		}
	}
	if hit && !open {
		return TriggerResult{SkipReason: SkipMachineBusy, HeldReason: "a requested review is queued for this PR"}, true
	}
	return TriggerResult{}, false
}
