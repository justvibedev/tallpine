package backlog

import (
	"context"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func requireOrchestrator(actor Actor) error {
	if actor.Kind != domain.BacklogOriginOrchestrator {
		return apierr.Forbidden("BACKLOG_RUN_FORBIDDEN", "Only the orchestrator can write the plan or item progress")
	}
	return nil
}

// SavePlan stores the orchestrator's plan. The first plan starts the run.
func (s *RunService) SavePlan(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in RunPlanInput) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	if err := requireOrchestrator(actor); err != nil {
		return RunView{}, err
	}
	run, err := s.getRun(ctx, projectID, ref)
	if err != nil {
		return RunView{}, err
	}
	if run.Status == domain.BacklogRunDone || run.Status == domain.BacklogRunStopping {
		return RunView{}, mapRunError(transitionError("run %s is %s, its plan is closed", run.Ref(), run.Status))
	}
	existing, err := s.store.ListBacklogRunItems(ctx, run.ID)
	if err != nil {
		return RunView{}, err
	}
	plan, err := s.validatePlan(ctx, run, existing, in)
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	now := s.now().UTC()
	saved, err := s.store.SaveBacklogRunPlan(ctx, run.ID, plan, now, func(r *domain.BacklogRun) error {
		// Checked again here: a Stop may have landed since the read above.
		if r.Status == domain.BacklogRunDone || r.Status == domain.BacklogRunStopping {
			return transitionError("run %s is %s, its plan is closed", r.Ref(), r.Status)
		}
		if r.Status == domain.BacklogRunPlanning {
			r.Status, r.StartedAt = domain.BacklogRunRunning, &now
		}
		return nil
	})
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	s.requestPlans(ctx, saved)
	return s.view(ctx, saved)
}

// Pause parks a running run. The owner or the orchestrator may pause; an owner
// click also nudges the orchestrator, which only reads state on its turns.
func (s *RunService) Pause(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string, in RunPauseInput) (RunView, error) {
	return s.control(ctx, projectID, actor, ref, "pause", func(run *domain.BacklogRun, _ []domain.BacklogRunItem) error {
		in.Reason = truncateRunes(in.Reason, domain.MaxBacklogHold)
		return pauseRun(run, in)
	})
}

// Resume continues a paused run.
func (s *RunService) Resume(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (RunView, error) {
	return s.control(ctx, projectID, actor, ref, "resume", func(run *domain.BacklogRun, _ []domain.BacklogRunItem) error {
		return resumeRun(run)
	})
}

// Stop finishes what is in flight and starts nothing new; a run with nothing in
// flight ends at once.
func (s *RunService) Stop(ctx context.Context, projectID domain.ProjectID, actor Actor, ref string) (RunView, error) {
	now := s.now().UTC()
	return s.control(ctx, projectID, actor, ref, "stop", func(run *domain.BacklogRun, items []domain.BacklogRunItem) error {
		if err := stopRun(run); err != nil {
			return err
		}
		if len(items) == 0 {
			finishRun(run, now)
			return nil
		}
		settle(run, items, now)
		return nil
	})
}

func (s *RunService) control(ctx context.Context, projectID domain.ProjectID, actor Actor, ref, action string, apply func(*domain.BacklogRun, []domain.BacklogRunItem) error) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	if actor.Kind != domain.BacklogOriginHuman && actor.Kind != domain.BacklogOriginOrchestrator {
		return RunView{}, apierr.Forbidden("BACKLOG_RUN_FORBIDDEN", "Only the owner or the orchestrator can control a run")
	}
	run, err := s.getRun(ctx, projectID, ref)
	if err != nil {
		return RunView{}, err
	}
	updated, _, err := s.store.UpdateBacklogRunState(ctx, run.ID, s.now().UTC(), func(r *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		return apply(r, items)
	})
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	if actor.Kind == domain.BacklogOriginHuman {
		// Best effort: the state change above is what counts.
		_ = s.notifyOrchestrator(ctx, projectID, runControlText(updated, action))
	}
	if action == "resume" {
		s.requestPlans(ctx, updated)
	}
	return s.view(ctx, updated)
}

// SetItem records one planned item's progress.
func (s *RunService) SetItem(ctx context.Context, projectID domain.ProjectID, actor Actor, ref, itemRef string, up RunItemUpdate) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	if err := requireOrchestrator(actor); err != nil {
		return RunView{}, err
	}
	up.Reason, up.Question = strings.TrimSpace(up.Reason), strings.TrimSpace(up.Question)
	if utf8.RuneCountInString(up.Reason) > domain.MaxBacklogRunReason || utf8.RuneCountInString(up.Question) > domain.MaxBacklogRunQuestion {
		return RunView{}, mapRunError(invalidRun("reason is at most %d and question at most %d characters", domain.MaxBacklogRunReason, domain.MaxBacklogRunQuestion))
	}
	run, err := s.getRun(ctx, projectID, ref)
	if err != nil {
		return RunView{}, err
	}
	item, err := s.resolveRunItem(ctx, projectID, itemRef)
	if err != nil {
		return RunView{}, err
	}
	blockers, err := s.blockerStates(ctx, item)
	if err != nil {
		return RunView{}, err
	}
	if up.Status == domain.BacklogRunItemActive {
		if err := s.checkPlanned(ctx, item); err != nil {
			return RunView{}, mapRunError(err)
		}
	}
	now := s.now().UTC()
	updated, _, err := s.store.UpdateBacklogRunState(ctx, run.ID, now, func(r *domain.BacklogRun, items []domain.BacklogRunItem, active int) error {
		for i := range items {
			if items[i].ItemID != item.ID {
				continue
			}
			if up.Status == domain.BacklogRunItemActive && items[i].Reason == domain.BacklogRunReasonPlanPending {
				return transitionError("%s waits for its plan to be approved", items[i].ItemRef())
			}
			if err := applyItemUpdate(*r, &items[i], up, active); err != nil {
				return err
			}
			if up.Status == domain.BacklogRunItemActive {
				if err := checkStartable(items, i, blockers); err != nil {
					return err
				}
			}
			settle(r, items, now)
			return nil
		}
		return invalidRun("%s is not in run %s", item.Ref(), run.Ref())
	})
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	// An item leaving flight frees a worker slot for a waiting planner.
	s.requestPlans(ctx, updated)
	return s.view(ctx, updated)
}

// Finish records the orchestrator's end-of-run narrative and ends the run.
func (s *RunService) Finish(ctx context.Context, projectID domain.ProjectID, actor Actor, ref, summary string) (RunView, error) {
	if err := s.requireEnabled(); err != nil {
		return RunView{}, err
	}
	if err := requireOrchestrator(actor); err != nil {
		return RunView{}, err
	}
	summary = strings.TrimSpace(summary)
	if utf8.RuneCountInString(summary) > domain.MaxBacklogRunSummary {
		return RunView{}, mapRunError(invalidRun("summary is at most %d characters", domain.MaxBacklogRunSummary))
	}
	run, err := s.getRun(ctx, projectID, ref)
	if err != nil {
		return RunView{}, err
	}
	now := s.now().UTC()
	updated, _, err := s.store.UpdateBacklogRunState(ctx, run.ID, now, func(r *domain.BacklogRun, items []domain.BacklogRunItem, _ int) error {
		return closeRun(r, items, summary, now)
	})
	if err != nil {
		return RunView{}, mapRunError(err)
	}
	return s.view(ctx, updated)
}
