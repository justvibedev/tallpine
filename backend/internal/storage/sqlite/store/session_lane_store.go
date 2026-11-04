package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// The store is the lane reader every lane-aware service type-asserts for.
var _ ports.LaneReader = (*Store)(nil)

// SetSessionLane records a session's lane and spawning parent. Both empty
// removes nothing: a binding is only written when there is something to bind.
func (s *Store) SetSessionLane(ctx context.Context, id domain.SessionID, lane domain.SessionLane) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.qw.SetSessionLane(ctx, gen.SetSessionLaneParams{
		SessionID: string(id), LaneItemID: lane.LaneItemID, ParentSessionID: string(lane.ParentSessionID),
	})
	if err != nil {
		return fmt.Errorf("set lane for %s: %w", id, err)
	}
	return nil
}

// SessionLane returns the session's lane binding; the zero value is un-laned.
func (s *Store) SessionLane(ctx context.Context, id domain.SessionID) (domain.SessionLane, error) {
	row, err := s.qr.GetSessionLane(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionLane{}, nil
	}
	if err != nil {
		return domain.SessionLane{}, fmt.Errorf("get lane for %s: %w", id, err)
	}
	return domain.SessionLane{LaneItemID: row.LaneItemID, ParentSessionID: domain.SessionID(row.ParentSessionID)}, nil
}

// ListSessionLanes returns the lane of every laned session of a project.
func (s *Store) ListSessionLanes(ctx context.Context, project domain.ProjectID) (map[domain.SessionID]string, error) {
	rows, err := s.qr.ListSessionLanesByProject(ctx, &project)
	if err != nil {
		return nil, fmt.Errorf("list lanes for %s: %w", project, err)
	}
	out := make(map[domain.SessionID]string, len(rows))
	for _, r := range rows {
		out[domain.SessionID(r.SessionID)] = r.LaneItemID
	}
	return out, nil
}

// ListPendingReportLanes returns the distinct lanes ("" = un-laned) holding a
// pending report of a project that is available at the given time.
func (s *Store) ListPendingReportLanes(ctx context.Context, project domain.ProjectID, at time.Time) ([]string, error) {
	lanes, err := s.qr.ListPendingReportLanes(ctx, gen.ListPendingReportLanesParams{ProjectID: string(project), AvailableAt: at})
	if err != nil {
		return nil, fmt.Errorf("list pending report lanes for %s: %w", project, err)
	}
	return lanes, nil
}

// BacklogRefsByID returns the display ref ("B12") of each known backlog item
// among ids in one query; unknown ids are absent from the map.
func (s *Store) BacklogRefsByID(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	rows, err := s.qr.SelectBacklogSeqsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list backlog refs: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.ID] = domain.FormatBacklogRef(r.Seq)
	}
	return out, nil
}
