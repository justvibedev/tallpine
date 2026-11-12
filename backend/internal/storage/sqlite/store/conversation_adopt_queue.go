package store

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// adoptForeignQueuedTurns hands the project conversation's queued turns that an
// earlier orchestrator still owns to session, the conversation's new owner. An
// owner message sent while the old orchestrator was handing off queues for it;
// left there it cannot bind under the successor's generation, and the
// successor's first drain fails on it (B667). Its rows move to fresh sequences
// after everything already recorded, including the context boundary the caller
// just wrote, so the successor's timeline shows the prompt it answers. Every
// rebind adopts, so a conversation wedged by an earlier build heals on the next
// start. It returns the new latest sequence, or 0 when nothing moved.
func adoptForeignQueuedTurns(ctx context.Context, q *gen.Queries, conversationID string, session domain.SessionID, now time.Time) (int64, error) {
	rows, err := q.SelectForeignQueuedTurnRows(ctx, gen.SelectForeignQueuedTurnRowsParams{
		ConversationID: conversationID, SessionID: session,
	})
	if err != nil {
		return 0, fmt.Errorf("select queued turns of earlier owners in %s: %w", conversationID, err)
	}
	var latest int64
	for _, row := range rows {
		latest, err = q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{UpdatedAt: now, ID: conversationID})
		if err != nil {
			return 0, fmt.Errorf("allocate sequence for adopted %s %s: %w", row.Kind, row.ID, err)
		}
		if row.Kind == "activity" {
			err = q.SetConversationActivitySequence(ctx, gen.SetConversationActivitySequenceParams{
				Sequence: latest, ID: row.ID, ConversationID: conversationID,
			})
		} else {
			err = q.SetConversationMessageSequence(ctx, gen.SetConversationMessageSequenceParams{
				Sequence: latest, ID: row.ID, ConversationID: conversationID,
			})
		}
		if err != nil {
			return 0, fmt.Errorf("move adopted %s %s: %w", row.Kind, row.ID, err)
		}
	}
	if err := q.AdoptQueuedConversationTurns(ctx, gen.AdoptQueuedConversationTurnsParams{
		SessionID: session, ConversationID: conversationID,
	}); err != nil {
		return 0, fmt.Errorf("adopt queued turns in %s: %w", conversationID, err)
	}
	return latest, nil
}
