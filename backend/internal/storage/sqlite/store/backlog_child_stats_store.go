package store

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// BacklogChildStats is how many children a parent has and how many of them are closed.
type BacklogChildStats struct {
	Total  int
	Closed int
}

// BacklogChildStats returns the child counts of every parent in a project that has children, by parent id.
func (s *Store) BacklogChildStats(ctx context.Context, projectID domain.ProjectID) (map[string]BacklogChildStats, error) {
	rows, err := s.qr.CountBacklogChildStatsByProject(ctx, string(projectID))
	if err != nil {
		return nil, err
	}
	out := make(map[string]BacklogChildStats, len(rows))
	for _, r := range rows {
		out[r.ParentID.String] = BacklogChildStats{Total: int(r.Total), Closed: int(r.Closed)}
	}
	return out, nil
}

// CountBacklogChildrenClosed counts one parent's done and dropped children.
func (s *Store) CountBacklogChildrenClosed(ctx context.Context, parentID string) (int, error) {
	n, err := s.qr.CountBacklogChildrenClosed(ctx, nullString(parentID))
	return int(n), err
}
