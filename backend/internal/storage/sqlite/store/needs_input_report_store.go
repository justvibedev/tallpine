package store

import (
	"context"
	"encoding/json"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// LatestNeedsInputReports returns each listed session's open needs_input
// report (its newest, unless a later done or stuck report answered it) in one
// statement, so a session list costs the same however many rows it has.
func (s *Store) LatestNeedsInputReports(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]domain.ReportRecord, error) {
	out := make(map[domain.SessionID]domain.ReportRecord)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.LatestOpenNeedsInputReports(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("latest needs-input reports: %w", err)
	}
	for _, row := range rows {
		id := domain.SessionID(row.SessionID)
		out[id] = domain.ReportRecord{ID: row.ID, SessionID: id, State: domain.ReportNeedsInput, Note: row.Note, CreatedAt: row.CreatedAt}
	}
	return out, nil
}
