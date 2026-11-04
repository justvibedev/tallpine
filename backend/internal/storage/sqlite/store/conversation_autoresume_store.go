package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ErrConversationTurnNotFound reports a turn id that is not in the conversation it
// was named against. Distinct from a rollback the provider refused: the caller
// pointed at nothing. It aliases the domain sentinel so a service can recognize it
// without importing the storage layer.
var ErrConversationTurnNotFound = domain.ErrNoConversationTurn

// TurnByID reads one turn. It returns ErrConversationTurnNotFound rather than a
// zero value, so a caller cannot mistake "no such turn" for "a turn with no
// provider id".
func (s *Store) TurnByID(ctx context.Context, turnID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, turnID)
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select turn %s: %w", turnID, err)
	}
	return turnToDomain(row), nil
}

// SetTurnAutoResume keeps user consent on the failed source, separate from the
// new attempt. The retry transaction consumes it before provider dispatch.
func (s *Store) SetTurnAutoResume(ctx context.Context, conversationID, turnID string, resetsAt *time.Time, enabled bool, resumeError string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if enabled || resumeError != "" {
		turn, err := s.qw.SelectConversationTurnByID(ctx, turnID)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrNoConversationTurn
		}
		if err != nil {
			return fmt.Errorf("read turn auto-resume consent: %w", err)
		}
		if turn.ConversationID != conversationID {
			return domain.ErrNoConversationTurn
		}
		// A manual retry or cancellation may have consumed the scanned intent.
		if !enabled && turn.UsageLimitAutoResume == 0 {
			return nil
		}
		if enabled {
			_, retryErr := s.qw.SelectConversationRetryTurnIDBySource(ctx, gen.SelectConversationRetryTurnIDBySourceParams{
				ConversationID: conversationID, RetryOfTurnID: sql.NullString{String: turnID, Valid: true},
			})
			if retryErr == nil {
				return domain.ErrNoConversationTurn
			}
			if !errors.Is(retryErr, sql.ErrNoRows) {
				return fmt.Errorf("read turn retry receipt: %w", retryErr)
			}
			changed, err := s.qw.SessionAgentAccountChangedSince(ctx, gen.SessionAgentAccountChangedSinceParams{
				SessionID: turn.HandledBySessionID, RequestedAt: turn.RequestedAt,
			})
			if err != nil {
				return fmt.Errorf("read turn account binding: %w", err)
			}
			if changed {
				return ports.ErrAgentAccountChanged
			}
		}
	}
	var armed int64
	if enabled {
		armed = 1
	}
	var reset sql.NullTime
	if resetsAt != nil {
		reset = timeToNullTime(*resetsAt)
	}
	n, err := s.qw.SetConversationTurnAutoResume(ctx, gen.SetConversationTurnAutoResumeParams{
		ID: turnID, ConversationID: conversationID, ResetsAt: reset, Enabled: armed, ResumeError: resumeError,
	})
	if err != nil {
		return fmt.Errorf("set turn auto-resume: %w", err)
	}
	if n == 0 {
		return domain.ErrNoConversationTurn
	}
	return nil
}

// PostponeTurnAutoResume moves an armed deadline without restoring consumed consent.
func (s *Store) PostponeTurnAutoResume(ctx context.Context, conversationID, turnID string, resetAt time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.PostponeConversationTurnAutoResume(ctx, gen.PostponeConversationTurnAutoResumeParams{
		ID: turnID, ConversationID: conversationID, ResetsAt: timeToNullTime(resetAt),
	})
	if err != nil {
		return false, fmt.Errorf("postpone turn auto-resume: %w", err)
	}
	return n > 0, nil
}

// AutoResumeTurns reads durable intent and deadlines so a sweep can skip future
// turns cheaply and validate their eligibility when due.
func (s *Store) AutoResumeTurns(ctx context.Context) ([]domain.ConversationTurn, error) {
	rows, err := s.qr.SelectAutoResumeConversationTurns(ctx)
	if err != nil {
		return nil, fmt.Errorf("read auto-resume turns: %w", err)
	}
	turns := make([]domain.ConversationTurn, 0, len(rows))
	for _, row := range rows {
		turns = append(turns, turnToDomain(row))
	}
	return turns, nil
}

// LatestAutoResumeTurn finds the latest visible turn that has left the queue.
func (s *Store) LatestAutoResumeTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectLatestAutoResumeConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("read latest auto-resume source: %w", err)
	}
	return turnToDomain(row), nil
}

// RollbackTurns records that a rollback discarded the named turn and everything
// after it, and returns how many turns that was.
//
// This is the Tallpine half of an operation the provider has already performed: the agent
// has forgotten those turns, and these rows are what stop the user being shown
// prose the agent cannot recall. The three statements commit together because a
// partial result is the one state that reintroduces the disagreement the whole
// operation exists to prevent.
//
// Nothing is deleted. The turns keep their rows and their immutable sequence
// positions, marked with when they were taken back.
func (s *Store) RollbackTurns(
	ctx context.Context,
	conversationID, turnID string,
	now time.Time,
) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	turn, err := s.qr.SelectConversationTurnByID(ctx, turnID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, turnID)
	}
	if err != nil {
		return 0, fmt.Errorf("select turn %s: %w", turnID, err)
	}
	if turn.ConversationID != conversationID {
		// A turn from another conversation would otherwise anchor a rollback by
		// rowid alone and discard an arbitrary range of this one.
		return 0, fmt.Errorf("%w: %s is not in conversation %s",
			ErrConversationTurnNotFound, turnID, conversationID)
	}

	var discarded int64
	err = s.inTx(ctx, "roll back turns", func(q *gen.Queries) error {
		rows, markErr := q.MarkConversationTurnsRolledBack(ctx,
			gen.MarkConversationTurnsRolledBackParams{
				RolledBackAt:   sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID,
				AnchorTurnID:   turnID,
			})
		if markErr != nil {
			return fmt.Errorf("mark turns rolled back: %w", markErr)
		}
		discarded = rows

		if err := q.InterruptRolledBackQueuedTurns(ctx,
			gen.InterruptRolledBackQueuedTurnsParams{
				CompletedAt:    sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("interrupt rolled back queued turns: %w", err)
		}

		if err := q.AttachLegacyCompactionsToRollbackAnchor(ctx,
			gen.AttachLegacyCompactionsToRollbackAnchorParams{
				AnchorTurnID:         sql.NullString{String: turnID, Valid: true},
				UpdatedAt:            now,
				TargetConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("correlate legacy compactions with rollback: %w", err)
		}

		if err := q.RecomputeConversationCompactedAt(ctx,
			gen.RecomputeConversationCompactedAtParams{
				UpdatedAt:            now,
				TargetConversationID: conversationID,
			}); err != nil {
			return fmt.Errorf("recompute compacted conversation state: %w", err)
		}

		if err := q.FailRolledBackConversationApprovals(ctx,
			gen.FailRolledBackConversationApprovalsParams{
				UpdatedAt:        now,
				ConversationID:   conversationID,
				ConversationID_2: conversationID,
			}); err != nil {
			return fmt.Errorf("fail rolled back approvals: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return int(discarded), nil
}

// SessionAccountChangedSince reports whether the session's agent account differs
// from the one it was bound to at since. A quota refusal belongs to the account
// that issued it, so a move to another account ends the pause.
func (s *Store) SessionAccountChangedSince(ctx context.Context, id domain.SessionID, since time.Time) (bool, error) {
	changed, err := s.qr.SessionAgentAccountChangedSince(ctx, gen.SessionAgentAccountChangedSinceParams{SessionID: id, RequestedAt: since})
	if err != nil {
		return false, fmt.Errorf("read session account binding: %w", err)
	}
	return changed, nil
}
