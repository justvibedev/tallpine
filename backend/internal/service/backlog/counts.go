package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// InboxCounts is the number of inbox items waiting for triage in each
// non-archived project; projects with an empty inbox are absent.
func (s *Service) InboxCounts(ctx context.Context) (map[domain.ProjectID]int, error) {
	raw, err := s.store.CountBacklogInboxByProject(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[domain.ProjectID]int, len(raw))
	for id, n := range raw {
		out[domain.ProjectID(id)] = n
	}
	return out, nil
}
