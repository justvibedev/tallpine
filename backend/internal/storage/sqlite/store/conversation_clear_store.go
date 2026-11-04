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

// RebindActiveBranchProvider moves a live Chat onto a replacement provider
// conversation that was opened in place (Codex /clear). The conversation's active
// branch and the session's provider handle move in one transaction, guarded on
// the previous id and the controller generation so a stale or replaced
// controller changes nothing. The session's native-history checkpoint is reset
// to the new owner by the same query branch activation uses.
func (s *Store) RebindActiveBranchProvider(
	ctx context.Context,
	sessionID domain.SessionID,
	conversationID, previous, next, generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTxDB(ctx, "rebind active branch provider", func(q *gen.Queries, db gen.DBTX) error {
		if db == nil {
			return errors.New("rebind active branch provider: needs its own transaction")
		}
		conversation, err := q.SelectConversationByID(ctx, conversationID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: conversation %s", domain.ErrNoConversationBranch, conversationID)
		}
		if err != nil {
			return fmt.Errorf("select conversation %s: %w", conversationID, err)
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != sessionID {
			return fmt.Errorf("conversation %s is not controlled by session %s", conversationID, sessionID)
		}
		res, err := db.ExecContext(ctx, `
UPDATE conversation_branches SET provider_conversation_id = ?
WHERE id = ? AND conversation_id = ? AND provider_conversation_id = ?`,
			next, conversation.ActiveBranchID, conversationID, previous)
		if err != nil {
			return fmt.Errorf("rebind branch %s provider: %w", conversation.ActiveBranchID, err)
		}
		if rows, err := res.RowsAffected(); err != nil || rows != 1 {
			return fmt.Errorf("rebind branch %s provider: active branch no longer on %q", conversation.ActiveBranchID, previous)
		}
		var owner string
		if err := db.QueryRowContext(ctx,
			`SELECT controller_generation FROM sessions WHERE id = ? AND session_mode = 'chat' AND provider_conversation_id = ?`,
			sessionID, previous).Scan(&owner); err != nil {
			return fmt.Errorf("session %s no longer owns %q: %w", sessionID, previous, err)
		}
		if owner != generation {
			return errors.New("controller owner changed")
		}
		rows, err := q.ActivateConversationBranchSession(ctx, gen.ActivateConversationBranchSessionParams{
			ProviderConversationID: next,
			ControllerGeneration:   generation,
			UpdatedAt:              now,
			ID:                     sessionID,
		})
		if err != nil {
			return fmt.Errorf("move session provider: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("move session provider: live chat session %s not found", sessionID)
		}
		return nil
	})
}
