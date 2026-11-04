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

// checkQueuedSession verifies, under writeMu, that the session can still queue
// a message: it is provisioning, hibernated, exited or (when starting is set)
// has a controller starting now, and still owns the conversation.
func (s *Store) checkQueuedSession(ctx context.Context, conversationID string, session domain.SessionID, starting bool) error {
	// A Send may have read "provisioning" just before Kill or Ready.
	// Session writes share writeMu, so this check and the append are atomic.
	// An exited agent (its host died with the previous daemon) queues for
	// the resume that drains it.
	record, readErr := s.qw.GetSession(ctx, session)
	if readErr != nil {
		return fmt.Errorf("check queued session %s: %w", session, readErr)
	}
	if record.IsTerminated || (!starting && !record.ProvisionState.IsProvisioning() && !record.HibernatedAt.Valid &&
		record.ActivityState != domain.ActivityExited) {
		return domain.ErrSessionNotProvisioning
	}
	// The caller read the conversation before taking writeMu. An orchestrator's
	// first controller may have moved the queue out of it since then; a turn
	// appended here would never drain or show.
	conversation, readErr := s.qw.SelectConversationByID(ctx, conversationID)
	if readErr != nil {
		return fmt.Errorf("check queued conversation %s: %w", conversationID, readErr)
	}
	if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != session {
		return domain.ErrConversationRebound
	}
	return nil
}

// duplicateUserMessage reports whether this retry or client message was
// already appended, and rejects a client id reused with a different payload.
func (s *Store) duplicateUserMessage(ctx context.Context, conversationID, retryOfTurnID string, msg domain.ConversationMessage) (bool, error) {
	if retryOfTurnID != "" {
		_, lookupErr := s.qr.SelectConversationRetryTurnIDBySource(ctx,
			gen.SelectConversationRetryTurnIDBySourceParams{
				ConversationID: conversationID,
				RetryOfTurnID:  sql.NullString{String: retryOfTurnID, Valid: true},
			})
		if lookupErr == nil {
			return true, nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return false, fmt.Errorf("lookup retry of turn %s: %w", retryOfTurnID, lookupErr)
		}
	} else if msg.ClientMessageID != "" {
		existing, lookupErr := s.qr.SelectConversationMessageByClientID(ctx,
			gen.SelectConversationMessageByClientIDParams{
				ConversationID:  conversationID,
				ClientMessageID: msg.ClientMessageID,
			})
		if lookupErr == nil {
			// Older rows have no intake fingerprint. Compare their current
			// persisted payload exactly; never guess through Tallpine-added context.
			if existing.ClientPayloadHash.Valid {
				if msg.ClientPayloadHash == "" || existing.ClientPayloadHash.String != msg.ClientPayloadHash {
					return false, domain.ErrClientMessageConflict
				}
			} else if existing.Text != msg.Text || existing.Origin != msg.Origin ||
				existing.DeliveryContentJson != msg.DeliveryContentJSON {
				return false, domain.ErrClientMessageConflict
			}
			return true, nil
		}
		if !errors.Is(lookupErr, sql.ErrNoRows) {
			return false, fmt.Errorf("lookup client message %s: %w", msg.ClientMessageID, lookupErr)
		}
	}
	return false, nil
}

// consumeDueAutoResume spends the source turn's auto-resume consent, or
// returns why it cannot be spent.
func consumeDueAutoResume(ctx context.Context, q *gen.Queries, conversationID string, session domain.SessionID, retryOfTurnID string, now time.Time) error {
	source, err := q.SelectConversationTurnByID(ctx, retryOfTurnID)
	if err != nil {
		return fmt.Errorf("read auto-resume source: %w", err)
	}
	changed, err := q.SessionAgentAccountChangedSince(ctx, gen.SessionAgentAccountChangedSinceParams{
		SessionID: source.HandledBySessionID, RequestedAt: source.RequestedAt,
	})
	if err != nil {
		return fmt.Errorf("read auto-resume account binding: %w", err)
	}
	if changed {
		return ports.ErrAgentAccountChanged
	}
	consumed, err := q.ConsumeConversationTurnAutoResume(ctx, gen.ConsumeConversationTurnAutoResumeParams{
		ID: retryOfTurnID, ConversationID: conversationID, SessionID: session, Now: timeToNullTime(now),
	})
	if err != nil {
		return fmt.Errorf("consume due auto-resume consent: %w", err)
	}
	if consumed == 0 {
		if source.UsageLimitAutoResume != 0 && source.UsageLimitResetsAt.Valid && source.UsageLimitResetsAt.Time.After(now) {
			return domain.ErrConversationAutoResumeNotDue
		}
		return domain.ErrNoConversationTurn
	}
	return nil
}
