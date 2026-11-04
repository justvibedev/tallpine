package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SetSessionPolicyDecision stores the agent policy decision record a session
// launched under, replacing the previous launch's.
func (s *Store) SetSessionPolicyDecision(ctx context.Context, id domain.SessionID, record []byte, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionPolicyDecision(ctx, gen.UpsertSessionPolicyDecisionParams{SessionID: string(id), Record: string(record), UpdatedAt: now}); err != nil {
		return fmt.Errorf("store policy decision for %s: %w", id, err)
	}
	return nil
}

// SessionPolicyDecision returns the stored decision record; ok is false when
// the session launched before the policy existed.
func (s *Store) SessionPolicyDecision(ctx context.Context, id domain.SessionID) (record []byte, updatedAt time.Time, ok bool, err error) {
	row, err := s.qr.GetSessionPolicyDecision(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, fmt.Errorf("read policy decision for %s: %w", id, err)
	}
	return []byte(row.Record), row.UpdatedAt, true, nil
}
