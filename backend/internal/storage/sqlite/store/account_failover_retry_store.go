package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// SaveAccountFailoverRetry records or updates a retry owed after an account move.
func (s *Store) SaveAccountFailoverRetry(ctx context.Context, r domain.AccountFailoverRetry) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var moved int64
	if r.Moved {
		moved = 1
	}
	if err := s.qw.UpsertAccountFailoverRetry(ctx, gen.UpsertAccountFailoverRetryParams{
		TurnID: r.TurnID, SessionID: string(r.SessionID), MoveID: r.MoveID, Moved: moved,
		Attempts: int64(r.Attempts), RetryAt: timeToNullTime(r.RetryAt), CreatedAt: r.CreatedAt,
	}); err != nil {
		return fmt.Errorf("save account failover retry: %w", err)
	}
	return nil
}

// DeleteAccountFailoverRetry removes a retry that was delivered or abandoned.
func (s *Store) DeleteAccountFailoverRetry(ctx context.Context, turnID string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.DeleteAccountFailoverRetry(ctx, turnID); err != nil {
		return fmt.Errorf("delete account failover retry: %w", err)
	}
	return nil
}

// ListAccountFailoverRetries returns every retry still owed, oldest first.
func (s *Store) ListAccountFailoverRetries(ctx context.Context) ([]domain.AccountFailoverRetry, error) {
	rows, err := s.qr.ListAccountFailoverRetries(ctx)
	if err != nil {
		return nil, fmt.Errorf("list account failover retries: %w", err)
	}
	out := make([]domain.AccountFailoverRetry, 0, len(rows))
	for _, row := range rows {
		r := domain.AccountFailoverRetry{
			TurnID: row.TurnID, SessionID: domain.SessionID(row.SessionID), MoveID: row.MoveID,
			Moved: row.Moved != 0, Attempts: int(row.Attempts), CreatedAt: row.CreatedAt,
		}
		if row.RetryAt.Valid {
			r.RetryAt = row.RetryAt.Time
		}
		out = append(out, r)
	}
	return out, nil
}
