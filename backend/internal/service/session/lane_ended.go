package session

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// endedLaneStore is the optional store capability that names the orchestrators
// of ended lanes.
type endedLaneStore interface {
	EndedLaneOrchestrators(context.Context) (map[domain.SessionID]bool, error)
}

// dropEndedLaneOrchestrators removes the orchestrators of ended lanes from an
// orchestrator list. They stay in session history, but an ended lane is gone:
// listed, it would show as a stopped orchestrator beside the default one.
func (s *Service) dropEndedLaneOrchestrators(ctx context.Context, recs []domain.SessionRecord) ([]domain.SessionRecord, error) {
	store, ok := s.store.(endedLaneStore)
	if !ok {
		return recs, nil
	}
	ended, err := store.EndedLaneOrchestrators(ctx)
	if err != nil {
		return nil, fmt.Errorf("list ended lanes: %w", err)
	}
	if len(ended) == 0 {
		return recs, nil
	}
	out := recs[:0:0]
	for _, rec := range recs {
		if !ended[rec.ID] {
			out = append(out, rec)
		}
	}
	return out, nil
}

// laneHoldStore is the optional store capability that keeps a lane open while
// a clean replacement retires its orchestrator.
type laneHoldStore interface {
	HoldLane(context.Context, domain.ProjectID, string) error
	ReleaseLane(context.Context, domain.ProjectID, string) error
}

// holdLaneForReplacement keeps lane open across a clean replacement, so
// retiring the old orchestrator does not end it. The returned release drops
// the hold and ends the lane if no orchestrator came up; it is a no-op when
// there is no lane or the store has no lanes. A failed hold fails the
// replacement: retiring without it would end the live lane.
func (s *Service) holdLaneForReplacement(ctx context.Context, project domain.ProjectID, lane string) (func(), error) {
	store, ok := s.store.(laneHoldStore)
	if lane == "" || !ok {
		return func() {}, nil
	}
	if err := store.HoldLane(ctx, project, lane); err != nil {
		return nil, toAPIError(err)
	}
	return func() {
		if err := store.ReleaseLane(ctx, project, lane); err != nil && s.logger != nil {
			s.logger.Warn("release lane after replacement", "project", project, "lane", lane, "err", err)
		}
	}, nil
}

// refuseEndedLaneRestore refuses to restore an ended lane's orchestrator: its
// lane is gone, so it would come back as a second default orchestrator.
func (s *Service) refuseEndedLaneRestore(ctx context.Context, id domain.SessionID) error {
	store, ok := s.store.(endedLaneStore)
	if !ok {
		return nil
	}
	ended, err := store.EndedLaneOrchestrators(ctx)
	if err != nil {
		return toAPIError(err)
	}
	if ended[id] {
		return apierr.Conflict("LANE_ENDED", "This lane orchestrator's lane has ended; start a new orchestrator instead", nil)
	}
	return nil
}
