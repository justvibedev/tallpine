package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// openBlockers lists the refs of the blocked_by targets in links that are not
// done or dropped.
func (s *Service) openBlockers(ctx context.Context, links []domain.BacklogLink) ([]string, error) {
	var refs []string
	for _, l := range links {
		if l.Kind != domain.BacklogLinkBlockedBy {
			continue
		}
		b, ok, err := s.store.GetBacklogItem(ctx, l.Ref)
		if err != nil {
			return nil, err
		}
		if ok && !b.State.Closed() {
			refs = append(refs, b.Ref())
		}
	}
	return refs, nil
}

// listOpenBlockers is openBlockers for a list-read item, which carries link
// counts but not links; only open items with a blocked_by link cost a read.
func (s *Service) listOpenBlockers(ctx context.Context, it domain.BacklogItem) ([]string, error) {
	if it.LinkCounts[domain.BacklogLinkBlockedBy] == 0 || it.State.Closed() {
		return nil, nil
	}
	full, ok, err := s.store.GetBacklogItem(ctx, it.ID)
	if err != nil || !ok {
		return nil, err
	}
	return s.openBlockers(ctx, full.Links)
}
