package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// RecordLastAssistantMessage keeps the final assistant message of a session's
// latest main turn for display. An empty message changes nothing, and a turn
// end older than the stored one never replaces it.
func (s *Store) RecordLastAssistantMessage(ctx context.Context, id domain.SessionID, message string, at time.Time) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.UpsertSessionLastAssistantMessage(ctx, gen.UpsertSessionLastAssistantMessageParams{
		SessionID: string(id), Message: message, At: at.UTC(),
	}); err != nil {
		return fmt.Errorf("record last assistant message: %w", err)
	}
	return nil
}

// LastAssistantMessage returns the stored final assistant message of the
// session's latest main turn; found is false when none was recorded.
func (s *Store) LastAssistantMessage(ctx context.Context, id domain.SessionID) (string, time.Time, bool, error) {
	row, err := s.qr.GetSessionLastAssistantMessage(ctx, string(id))
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("read last assistant message: %w", err)
	}
	return row.Message, row.At, true, nil
}
