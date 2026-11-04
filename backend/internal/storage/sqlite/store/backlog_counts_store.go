package store

import "context"

// CountBacklogInboxByProject returns the inbox size of every non-archived
// project that has at least one inbox item, keyed by project id.
func (s *Store) CountBacklogInboxByProject(ctx context.Context) (map[string]int, error) {
	rows, err := s.qr.CountBacklogInboxByProject(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.ProjectID] = int(r.Inbox)
	}
	return out, nil
}
