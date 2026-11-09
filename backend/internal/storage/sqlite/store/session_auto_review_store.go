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

// SetSessionAutoReview persists the daemon-side review automation toggle.
func (s *Store) SetSessionAutoReview(ctx context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAutoReview(ctx, gen.SetSessionAutoReviewParams{
		AutoReviewEnabled: enabled,
		UpdatedAt:         updatedAt,
		ID:                id,
	})
	if err != nil {
		return false, fmt.Errorf("set auto review for %s: %w", id, err)
	}
	if rows > 0 {
		if _, err := s.writeDB.ExecContext(ctx, `INSERT INTO session_auto_review_override (session_id, enabled) VALUES (?, ?)
ON CONFLICT(session_id) DO UPDATE SET enabled = excluded.enabled`, id, enabled); err != nil {
			return false, fmt.Errorf("record auto review override for %s: %w", id, err)
		}
	}
	return rows > 0, nil
}

// SessionAutoReviewOverride reports a deliberate per-session Auto review choice;
// found is false when the session follows the project or app setting.
func (s *Store) SessionAutoReviewOverride(ctx context.Context, id domain.SessionID) (enabled, found bool, err error) {
	err = s.readDB.QueryRowContext(ctx, `SELECT enabled FROM session_auto_review_override WHERE session_id = ?`, id).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return enabled, err == nil, err
}

// SyncSessionAutoReviewFlag rewrites the stored flag only, leaving any
// per-session override untouched, so it follows the project or app setting.
func (s *Store) SyncSessionAutoReviewFlag(ctx context.Context, id domain.SessionID, enabled bool, updatedAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.SetSessionAutoReview(ctx, gen.SetSessionAutoReviewParams{AutoReviewEnabled: enabled, UpdatedAt: updatedAt, ID: id})
	if err != nil {
		return false, fmt.Errorf("sync auto review flag for %s: %w", id, err)
	}
	return rows > 0, nil
}
