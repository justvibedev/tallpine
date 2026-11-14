package session

import (
	"context"
	"errors"
	"fmt"
	"os"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/sessionartifacts"
)

// EndLaneOutcome reports an ended lane.
type EndLaneOutcome struct {
	// Removed is the lane orchestrator that was deleted.
	Removed domain.SessionID `json:"removed"`
	// Orchestrator is the default orchestrator the lane's workers moved to.
	Orchestrator domain.SessionID `json:"orchestrator"`
	// Workers are the live workers that moved.
	Workers []domain.SessionID `json:"workers"`
	// NotifyFailed lists workers that moved but did not receive the notice.
	NotifyFailed []domain.SessionID `json:"notifyFailed"`
}

// laneEndCommander is the manager surface EndLane needs beyond commander.
type laneEndCommander interface {
	CheckWorkspaceSettled(ctx context.Context, id domain.SessionID) error
	RemoveSettledWorkspace(ctx context.Context, id domain.SessionID) error
}

// laneEndStore is the store surface EndLane needs.
type laneEndStore interface {
	EndLane(ctx context.Context, project domain.ProjectID, lane string, orchestrator domain.SessionID) error
	SessionLane(ctx context.Context, id domain.SessionID) (domain.SessionLane, error)
	ListSessionLanes(ctx context.Context, project domain.ProjectID) (map[domain.SessionID]string, error)
}

// FindLaneOrchestrator returns the newest live orchestrator of one lane.
func (s *Service) FindLaneOrchestrator(ctx context.Context, projectID domain.ProjectID, laneItemID string) (domain.Session, bool, error) {
	active, err := s.activeOrchestrators(ctx, projectID, laneItemID)
	if err != nil || len(active) == 0 {
		return domain.Session{}, false, err
	}
	return newestSession(active), true, nil
}

// EndLane fully removes a lane orchestrator: it terminates it, deletes its
// worktree, branch, artifacts and session record, and moves the lane's live
// workers and pending reports to the project's default orchestrator (started
// when none runs). The backlog item is untouched. It refuses, changing nothing,
// when the orchestrator's worktree holds uncommitted or unpushed work. Safe to
// repeat after a partial failure: an already-terminated orchestrator is not
// killed again. With dryRun it runs the checks only and reports the live
// workers that would move.
func (s *Service) EndLane(ctx context.Context, id domain.SessionID, dryRun bool) (EndLaneOutcome, error) {
	ctx = context.WithoutCancel(ctx)
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return EndLaneOutcome{}, toAPIError(err)
	}
	if !ok {
		return EndLaneOutcome{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.Kind != domain.KindOrchestrator {
		return EndLaneOutcome{}, apierr.Invalid("NOT_AN_ORCHESTRATOR", "Only a lane orchestrator can be ended", nil)
	}
	ls, lok := s.store.(laneEndStore)
	cmd, cok := s.manager.(laneEndCommander)
	if !lok || !cok {
		return EndLaneOutcome{}, apierr.Invalid("LANES_UNAVAILABLE", "Lanes are not supported by this store", nil)
	}
	lane, err := ls.SessionLane(ctx, id)
	if err != nil {
		return EndLaneOutcome{}, toAPIError(err)
	}
	if lane.LaneItemID == "" {
		return EndLaneOutcome{}, apierr.Invalid("NOT_A_LANE_ORCHESTRATOR",
			"This is the project's default orchestrator, not a lane orchestrator; there is no lane to end", nil)
	}
	project, err := s.requireProject(ctx, rec.ProjectID)
	if err != nil {
		return EndLaneOutcome{}, err
	}

	unlock := s.lockOrchestratorProject(rec.ProjectID)
	defer unlock()

	if err := s.checkLaneOpenPRs(ctx, ls, rec.ProjectID, id, lane.LaneItemID); err != nil {
		return EndLaneOutcome{}, err
	}

	// Checked whether or not the orchestrator is still running: an archived
	// lane orchestrator can have a kept worktree with work in it, and deleting
	// its record would orphan that work.
	if err := cmd.CheckWorkspaceSettled(ctx, id); err != nil {
		return EndLaneOutcome{}, workHeldError(err, "")
	}
	if dryRun {
		workers, err := s.liveLaneWorkers(ctx, ls, rec.ProjectID, id, lane.LaneItemID)
		return EndLaneOutcome{Removed: id, Workers: workers}, err
	}

	// The default orchestrator first, so a failure to start it leaves the lane
	// untouched. If a later step fails it stays running, which is harmless.
	def, found, err := s.FindLaneOrchestrator(ctx, rec.ProjectID, "")
	if err != nil {
		return EndLaneOutcome{}, err
	}
	if !found {
		def, err = s.spawnOrchestratorLocked(ctx, project, false, rec.Mode, "", orchestratorSpawnOptions{})
		if err != nil {
			return EndLaneOutcome{}, err
		}
	}

	// Read before the kill: terminating the orchestrator ends its lane (the
	// session_lane_end_on_terminate trigger), which drops the workers' lane
	// membership. A worker spawned after this read is still re-homed, but gets
	// no notice; its reports reach the default orchestrator.
	workers, err := s.liveLaneWorkers(ctx, ls, rec.ProjectID, id, lane.LaneItemID)
	if err != nil {
		return EndLaneOutcome{}, err
	}

	// Stop the agent (non-force teardown keeps a dirty worktree), then check
	// again: nothing can commit now, so this result is final.
	if !rec.IsTerminated {
		if _, err := s.Kill(ctx, id); err != nil {
			return EndLaneOutcome{}, err
		}
	}
	if err := cmd.RemoveSettledWorkspace(ctx, id); err != nil {
		// The kill above already ran: say the orchestrator is stopped, not ended.
		return EndLaneOutcome{}, workHeldError(err, id)
	}
	if s.artifactDeleter != nil {
		if err := s.DeleteArtifacts(ctx, id); err != nil {
			return EndLaneOutcome{}, err
		}
	}
	if dir := sessionartifacts.ResolveManaged(s.dataDir, id, rec.Metadata.ArtifactDir); dir != "" {
		if err := os.RemoveAll(dir); err != nil {
			return EndLaneOutcome{}, fmt.Errorf("end lane: remove artifacts of %s: %w", id, err)
		}
	}
	if err := ls.EndLane(ctx, rec.ProjectID, lane.LaneItemID, id); err != nil {
		return EndLaneOutcome{}, toAPIError(err)
	}

	if s.laneEnded != nil {
		s.laneEnded(ctx, rec.ProjectID, lane.LaneItemID)
	}
	out := EndLaneOutcome{Removed: id, Orchestrator: def.ID, Workers: workers}
	notice := fmt.Sprintf("Your lane orchestrator %s was removed and its lane ended. Your orchestrator is now %s: send reports and questions there (tallpine send --session %s), not to %s.", id, def.ID, def.ID, id)
	for _, w := range workers {
		if err := s.manager.Send(ctx, w, notice, nil); err != nil {
			out.NotifyFailed = append(out.NotifyFailed, w)
		}
	}
	return out, nil
}

// liveLaneWorkers lists the live workers bound to the lane.
func (s *Service) liveLaneWorkers(ctx context.Context, ls laneEndStore, project domain.ProjectID, orch domain.SessionID, lane string) ([]domain.SessionID, error) {
	lanes, err := ls.ListSessionLanes(ctx, project)
	if err != nil {
		return nil, toAPIError(err)
	}
	active := true
	live, err := s.List(ctx, ListFilter{ProjectID: project, Active: &active})
	if err != nil {
		return nil, err
	}
	var workers []domain.SessionID
	for _, sess := range live {
		if sess.ID != orch && sess.Kind != domain.KindOrchestrator && lanes[sess.ID] == lane {
			workers = append(workers, sess.ID)
		}
	}
	return workers, nil
}

// workHeldError refuses with the worktree's unsaved work. A non-empty stopped
// names a lane orchestrator that was already killed, so the message says it was
// left stopped and how to resume it.
func workHeldError(err error, stopped domain.SessionID) error {
	if errors.Is(err, sessionmanager.ErrWorkspaceHoldsWork) {
		msg := "This lane orchestrator's worktree has uncommitted or unpushed changes (" + err.Error() + "); commit or push them, or discard them, then end the lane again"
		if stopped != "" {
			msg += ". The lane orchestrator " + string(stopped) + " was left stopped, not ended: open it and choose Resume agent to keep working in the lane"
		}
		return apierr.Conflict("LANE_WORKSPACE_HAS_WORK", msg, nil)
	}
	return toAPIError(err)
}
