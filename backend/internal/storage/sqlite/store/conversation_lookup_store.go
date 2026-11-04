package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ProjectConversation is a read-only lookup; unlike CreateConversation it does
// not transfer ownership before a replacement provider has connected.
func (s *Store) ProjectConversation(ctx context.Context, project domain.ProjectID) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectProjectConversation(ctx, optionalProjectID(project))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select project conversation: %w", err)
	}
	return conversationToDomain(row), nil
}

// SessionForActivityProviderItem finds the session holding an action activity.
func (s *Store) SessionForActivityProviderItem(ctx context.Context, providerItemID string) (domain.SessionID, bool, error) {
	sessionID, err := s.qr.SelectSessionForActivityProviderItem(ctx, providerItemID)
	if errors.Is(err, sql.ErrNoRows) || sessionID == nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return *sessionID, true, nil
}

// ConversationForSession looks up a session's conversation.
func (s *Store) ConversationForSession(
	ctx context.Context,
	session domain.SessionID,
) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectConversationBySession(ctx, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select conversation for %s: %w", session, err)
	}
	return conversationToDomain(row), nil
}

// ListInterruptedSessions reports which of the given sessions had their latest
// Chat turn interrupted by the person. Sessions without a Chat conversation are
// absent from the result.
func (s *Store) ListInterruptedSessions(ctx context.Context, ids []domain.SessionID) (map[domain.SessionID]bool, error) {
	out := make(map[domain.SessionID]bool)
	if len(ids) == 0 {
		return out, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, fmt.Errorf("marshal session ids: %w", err)
	}
	rows, err := s.qr.ListInterruptedSessions(ctx, string(encoded))
	if err != nil {
		return nil, fmt.Errorf("list interrupted sessions: %w", err)
	}
	for _, id := range rows {
		out[domain.SessionID(id)] = true
	}
	return out, nil
}

// ConversationForReview looks up the durable conversation owned by a reviewer
// row rather than by its parent worker session.
func (s *Store) ConversationForReview(ctx context.Context, reviewID string) (domain.ConversationRecord, error) {
	row, err := s.qr.SelectConversationByReview(ctx, nullableString(reviewID))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationRecord{}, ErrConversationNotFound
	}
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("select conversation for review %s: %w", reviewID, err)
	}
	return conversationToDomain(row), nil
}

// HasConversationTurns includes hidden and settled turns: an empty visible
// timeline is not proof that the provider conversation never started.
func (s *Store) HasConversationTurns(ctx context.Context, conversationID string) (bool, error) {
	hasTurns, err := s.qr.HasConversationTurns(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("check conversation turns %s: %w", conversationID, err)
	}
	return hasTurns, nil
}

// LatestConversationTurn reads the latest non-rolled-back turn on the active
// branch lineage without loading messages or activities.
func (s *Store) LatestConversationTurn(ctx context.Context, conversationID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectLatestConversationTurn(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select latest conversation turn: %w", err)
	}
	return turnToDomain(row), nil
}

// TurnByProviderID is the conversation's turn the provider knows as
// providerTurnID.
func (s *Store) TurnByProviderID(ctx context.Context, conversationID, providerTurnID string) (domain.ConversationTurn, error) {
	row, err := s.qr.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
		ConversationID: conversationID, ProviderTurnID: providerTurnID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationTurn{}, domain.ErrNoConversationTurn
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("select turn by provider id %s: %w", providerTurnID, err)
	}
	return turnToDomain(row), nil
}

// LatestVisibleUserTurnSettled checks the current branch's latest user prompt
// without loading the full conversation history.
func (s *Store) LatestVisibleUserTurnSettled(ctx context.Context, conversationID string, sessionID domain.SessionID) (bool, error) {
	settled, err := s.qr.LatestVisibleUserTurnSettled(ctx, gen.LatestVisibleUserTurnSettledParams{
		ConversationID: conversationID,
		SessionID:      sessionID,
	})
	if err != nil {
		return false, fmt.Errorf("check latest user turn for %s: %w", conversationID, err)
	}
	return settled, nil
}
