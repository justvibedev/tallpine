package report

import (
	"context"
	"slices"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// OrchestratorAwaits reports whether worker's reports go to an orchestrator
// that is idle (not mid-turn) and waiting on that worker, so a chat
// turn_ended need not wait out its quiet window (Q23).
//
// The receiving orchestrator is the one report delivery routes the worker to
// (domain.PickOrchestrator over the worker's lane). It is idle when its
// activity is ActivityIdle; an active, blocked or input-waiting orchestrator is
// mid-turn and keeps the hold, so a worker that resumes within the window still
// causes no spurious wake (B192, B255). It waits on the worker when the worker
// is live and its newest worker-filed report is not done: a worker that has
// reported done is not awaited. Any lookup failure answers false, which keeps
// the hold.
func (s *Service) OrchestratorAwaits(ctx context.Context, worker domain.SessionRecord) bool {
	if worker.Kind != domain.KindWorker || worker.IsTerminated || worker.Activity.State == domain.ActivityExited {
		return false
	}
	store, ok := s.store.(interface {
		ListSessions(context.Context, domain.ProjectID) ([]domain.SessionRecord, error)
		SessionReportLister
	})
	if !ok {
		return false
	}
	recs, err := store.ListSessions(ctx, worker.ProjectID)
	if err != nil {
		return false
	}
	var lanes map[domain.SessionID]string
	if ls, isLane := s.store.(LaneStore); isLane {
		if lanes, err = ls.ListSessionLanes(ctx, worker.ProjectID); err != nil {
			return false
		}
	}
	target, ok := domain.PickOrchestrator(recs, lanes, lanes[worker.ID])
	if !ok || target.Activity.State != domain.ActivityIdle {
		return false
	}
	reports, err := store.ListReportsBySession(ctx, worker.ID)
	if err != nil {
		return false
	}
	for _, r := range slices.Backward(reports) {
		if !r.Synthesized {
			return r.State != domain.ReportDone
		}
	}
	return true
}
