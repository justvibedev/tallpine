package store

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ListVisibleRunningTurnProviderIDs returns the same active-branch running turns,
// in the same order, that a conversation snapshot exposes to clients. Interrupt
// uses the full set because a root and nested provider turn may overlap; settling
// only one would leave the UI's Working state behind.
func (s *Store) ListVisibleRunningTurnProviderIDs(
	ctx context.Context,
	conversationID string,
) ([]string, error) {
	rows, err := s.qr.ListVisibleRunningTurnsForConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list visible running turns for %s: %w", conversationID, err)
	}
	providerTurnIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		providerTurnIDs = append(providerTurnIDs, row.ProviderTurnID)
	}
	return providerTurnIDs, nil
}

// ListVisibleRunningTurns returns the running turns of the active branch as a
// conversation snapshot presents them. A live reconnect needs only these: the
// whole snapshot of a long conversation could outlast its recovery deadline and
// strand the session without a controller (B810).
func (s *Store) ListVisibleRunningTurns(ctx context.Context, conversationID string) ([]domain.ConversationTurn, error) {
	rows, err := s.qr.ListVisibleRunningTurnsForConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("list visible running turns for %s: %w", conversationID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	presentation := s.conversationHistoryPresentation(ctx, conversationID)
	turns := make([]domain.ConversationTurn, 0, len(rows))
	for _, row := range rows {
		turn := turnToDomain(row)
		presentation.filterInactiveProviderTurn(&turn)
		turns = append(turns, turn)
	}
	return turns, nil
}
