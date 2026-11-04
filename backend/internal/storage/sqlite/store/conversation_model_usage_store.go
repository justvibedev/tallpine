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

// AddModelUsage adds one turn's per-model spend to the conversation's running
// totals. Unlike RecordUsage this accumulates: the totals are the sum of every
// turn, so a restart must not reset them.
func (s *Store) AddModelUsage(ctx context.Context, conversationID string, turn []domain.ModelUsage, now time.Time) error {
	if len(turn) == 0 {
		return nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	for _, u := range turn {
		if u.Model == "" {
			continue
		}
		if err := q.AddConversationModelUsage(ctx, gen.AddConversationModelUsageParams{
			ConversationID: conversationID, Model: u.Model,
			InputTokens: u.InputTokens, CacheCreationTokens: u.CacheCreationTokens,
			CacheReadTokens: u.CacheReadTokens, OutputTokens: u.OutputTokens, Credits: u.Credits, UpdatedAt: now,
		}); err != nil {
			return fmt.Errorf("add model usage for %s: %w", conversationID, err)
		}
	}
	return nil
}

// ModelUsageForSession returns the per-model totals of the session's conversation.
func (s *Store) ModelUsageForSession(ctx context.Context, session domain.SessionID) ([]domain.ModelUsage, error) {
	conv, err := s.conversationReader(ctx).SelectConversationBySession(ctx, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return []domain.ModelUsage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find conversation of %s: %w", session, err)
	}
	rows, err := s.conversationReader(ctx).ListConversationModelUsage(ctx, conv.ID)
	if err != nil {
		return nil, fmt.Errorf("list model usage of %s: %w", session, err)
	}
	out := make([]domain.ModelUsage, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.ModelUsage{
			Model: r.Model, InputTokens: r.InputTokens, CacheCreationTokens: r.CacheCreationTokens,
			CacheReadTokens: r.CacheReadTokens, OutputTokens: r.OutputTokens, Credits: r.Credits, Turns: r.Turns,
		})
	}
	return out, nil
}

// FinishedTurnsForSession counts the turns the provider finished in the
// session's conversation, whether or not the harness reports spend by model.
func (s *Store) FinishedTurnsForSession(ctx context.Context, session domain.SessionID) (int64, error) {
	conv, err := s.conversationReader(ctx).SelectConversationBySession(ctx, &session)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("find conversation of %s: %w", session, err)
	}
	n, err := s.conversationReader(ctx).CountFinishedConversationTurns(ctx, conv.ID)
	if err != nil {
		return 0, fmt.Errorf("count turns of %s: %w", session, err)
	}
	return n, nil
}
