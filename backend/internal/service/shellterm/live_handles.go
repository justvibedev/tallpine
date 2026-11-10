package shellterm

import "context"

// LiveHandleIDs lists the runtime handles of the terminals this app run owns.
func (s *Service) LiveHandleIDs(ctx context.Context) ([]string, error) {
	records, err := s.store.SelectRestorableShellTerminals(ctx, s.appRunID)
	if err != nil {
		return nil, err
	}
	handles := make([]string, 0, len(records))
	for _, rec := range records {
		handles = append(handles, rec.HandleID)
	}
	return handles, nil
}
