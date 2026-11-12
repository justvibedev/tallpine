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

// ConversationBranch reads one branch and reports whether it is active.
func (s *Store) ConversationBranch(
	ctx context.Context,
	conversationID, branchID string,
) (domain.ConversationBranch, error) {
	row, err := s.qr.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
		ConversationID: conversationID,
		BranchID:       branchID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationBranch{}, fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
	}
	if err != nil {
		return domain.ConversationBranch{}, fmt.Errorf("select conversation branch %s: %w", branchID, err)
	}
	return conversationBranchToDomain(row), nil
}

// ConversationBranches lists every durable sibling and descendant in creation
// order. Only the current head is marked active.
func (s *Store) ConversationBranches(
	ctx context.Context,
	conversationID string,
) ([]domain.ConversationBranch, error) {
	rows, err := s.snapshotReader(ctx).SelectConversationBranches(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("select conversation branches for %s: %w", conversationID, err)
	}
	branches := make([]domain.ConversationBranch, 0, len(rows))
	for _, row := range rows {
		branches = append(branches, conversationBranchListToDomain(row))
	}
	return branches, nil
}

// ConversationEditAnchor resolves the active-lineage fork point and preserves
// the original structured delivery content for the replacement send.
func (s *Store) ConversationEditAnchor(
	ctx context.Context,
	conversationID, replacedTurnID string,
) (domain.ConversationEditAnchor, error) {
	row, err := s.qr.SelectConversationEditAnchor(ctx, gen.SelectConversationEditAnchorParams{
		ConversationID: conversationID,
		ReplacedTurnID: nullableString(replacedTurnID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationEditAnchor{}, fmt.Errorf("%w: %s", ErrConversationTurnNotFound, replacedTurnID)
	}
	if err != nil {
		return domain.ConversationEditAnchor{}, fmt.Errorf("select conversation edit anchor %s: %w", replacedTurnID, err)
	}
	return domain.ConversationEditAnchor{
		ConversationID:              row.ConversationID,
		SourceBranchID:              row.SourceBranchID,
		ReplacedTurnID:              row.ReplacedTurnID.String,
		PreviousProviderTurnID:      row.PreviousProviderTurnID,
		ForkAfterSequence:           row.ForkAfterSequence,
		ReplayFloorSequence:         row.ReplayFloorSequence,
		HasPriorContext:             row.HasPriorContext,
		OriginalDeliveryContentJSON: row.OriginalDeliveryContentJson,
		RetryActiveBranch:           row.RetryActiveBranch.Valid && row.RetryActiveBranch.Bool,
	}, nil
}

// UpdateConversationBranchReplacement attaches the first turn created on a
// child branch to the source prompt it replaces.
func (s *Store) UpdateConversationBranchReplacement(
	ctx context.Context,
	branchID, replacementTurnID string,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateConversationBranchReplacement(ctx,
		gen.UpdateConversationBranchReplacementParams{
			ReplacementTurnID: nullableString(replacementTurnID),
			BranchID:          branchID,
		})
	if err != nil {
		return fmt.Errorf("update conversation branch %s replacement: %w", branchID, err)
	}
	if rows != 1 {
		return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
	}
	return nil
}

// RepairIncompleteConversationEdit closes the only crash window in edit branch
// creation. If the active edit child already owns a durable human prompt, that
// earliest prompt becomes its replacement. If no prompt was recorded, the child
// was never dispatched and the conversation plus session provider ownership move
// back to the source branch in the same transaction.
//
// A non-empty returned branch means a repair was applied. restoredProviderOwner
// is true only when the abandoned child belongs to the same Tallpine session, so
// startup may replace that session's stale child provider handle. A project
// conversation can be rebound to a new orchestrator session; that new owner must
// start its own provider boundary rather than inherit the prior agent's handle.
func (s *Store) RepairIncompleteConversationEdit(
	ctx context.Context,
	sessionID domain.SessionID,
	conversationID string,
	now time.Time,
) (repaired domain.ConversationBranch, restoredProviderOwner bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.inTx(ctx, "repair incomplete conversation edit", func(q *gen.Queries) error {
		conversation, selectErr := q.SelectConversationByID(ctx, conversationID)
		if selectErr != nil {
			return fmt.Errorf("select conversation %s: %w", conversationID, selectErr)
		}
		if conversation.CurrentSessionID == nil || *conversation.CurrentSessionID != sessionID {
			return fmt.Errorf("conversation %s is not controlled by session %s", conversationID, sessionID)
		}
		activeRow, selectErr := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       conversation.ActiveBranchID,
		})
		if selectErr != nil {
			return fmt.Errorf("select active conversation branch %s: %w",
				conversation.ActiveBranchID, selectErr)
		}
		active := conversationBranchToDomain(activeRow)
		if active.ParentBranchID == "" || active.ReplacedTurnID == "" || active.ReplacementTurnID != "" {
			return nil
		}

		replacementTurnID, selectErr := q.SelectFirstHumanTurnOnBranch(
			ctx, gen.SelectFirstHumanTurnOnBranchParams{
				ConversationID: conversationID,
				BranchID:       active.ID,
			})
		if selectErr == nil {
			rows, updateErr := q.UpdateConversationBranchReplacement(
				ctx, gen.UpdateConversationBranchReplacementParams{
					ReplacementTurnID: nullableString(replacementTurnID),
					BranchID:          active.ID,
				})
			if updateErr != nil {
				return fmt.Errorf("link recovered edit branch %s: %w", active.ID, updateErr)
			}
			if rows != 1 {
				return fmt.Errorf("link recovered edit branch %s: branch disappeared", active.ID)
			}
			active.ReplacementTurnID = replacementTurnID
			repaired = active
			return nil
		}
		if !errors.Is(selectErr, sql.ErrNoRows) {
			return fmt.Errorf("select first prompt on edit branch %s: %w", active.ID, selectErr)
		}

		parentRow, selectErr := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       active.ParentBranchID,
		})
		if selectErr != nil {
			return fmt.Errorf("select source conversation branch %s: %w",
				active.ParentBranchID, selectErr)
		}
		parent := conversationBranchToDomain(parentRow)
		conversationRows, updateErr := q.ActivateConversationBranch(
			ctx, gen.ActivateConversationBranchParams{
				ActiveBranchID: parent.ID,
				UpdatedAt:      now,
				ID:             conversationID,
			})
		if updateErr != nil {
			return fmt.Errorf("restore source conversation head: %w", updateErr)
		}
		if conversationRows != 1 {
			return fmt.Errorf("restore source conversation head: conversation %s not found", conversationID)
		}
		parent.Active = true
		repaired = parent
		if active.SessionID != sessionID {
			// CreateConversation may have just rebound a project narrative to a new
			// orchestrator. Repair the lineage, but never copy the prior agent's
			// opaque provider handle into this new session.
			return nil
		}
		sessionRows, updateErr := q.ActivateConversationBranchSession(
			ctx, gen.ActivateConversationBranchSessionParams{
				ProviderConversationID: parent.ProviderConversationID,
				ControllerGeneration:   "",
				UpdatedAt:              now,
				ID:                     sessionID,
			})
		if updateErr != nil {
			return fmt.Errorf("restore source session controller: %w", updateErr)
		}
		if sessionRows != 1 {
			return fmt.Errorf("restore source session controller: live chat session %s not found", sessionID)
		}
		restoredProviderOwner = true
		return nil
	})
	if err != nil {
		return domain.ConversationBranch{}, false, err
	}
	return repaired, restoredProviderOwner, nil
}

// ActivateConversationBranch moves the durable conversation head and the Chat
// controller's provider handle/generation together. If the session is not a live
// Chat session, the conversation update is rolled back.
func (s *Store) ActivateConversationBranch(
	ctx context.Context,
	sessionID domain.SessionID,
	conversationID, branchID, providerConversationID, generation string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	return s.inTx(ctx, "activate conversation branch", func(q *gen.Queries) error {
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
		branch, err := q.SelectConversationBranch(ctx, gen.SelectConversationBranchParams{
			ConversationID: conversationID,
			BranchID:       branchID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, branchID)
		}
		if err != nil {
			return fmt.Errorf("select conversation branch %s: %w", branchID, err)
		}
		if branch.ProviderConversationID != providerConversationID {
			return fmt.Errorf("activate conversation branch %s: provider conversation %q does not match stored %q",
				branchID, providerConversationID, branch.ProviderConversationID)
		}
		conversationRows, err := q.ActivateConversationBranch(ctx, gen.ActivateConversationBranchParams{
			ActiveBranchID: branchID,
			UpdatedAt:      now,
			ID:             conversationID,
		})
		if err != nil {
			return fmt.Errorf("move conversation head: %w", err)
		}
		if conversationRows != 1 {
			return fmt.Errorf("move conversation head: conversation %s not found", conversationID)
		}
		sessionRows, err := q.ActivateConversationBranchSession(ctx,
			gen.ActivateConversationBranchSessionParams{
				ProviderConversationID: providerConversationID,
				ControllerGeneration:   generation,
				UpdatedAt:              now,
				ID:                     sessionID,
			})
		if err != nil {
			return fmt.Errorf("move session controller: %w", err)
		}
		if sessionRows != 1 {
			return fmt.Errorf("move session controller: live chat session %s not found", sessionID)
		}
		return nil
	})
}

// RebindUnansweredBranchProvider points a branch whose agent never answered a
// turn (see domain.TurnState.Answered) at the provider conversation that
// replaced one its provider never saved.
func (s *Store) RebindUnansweredBranchProvider(ctx context.Context, conversationID, branchID, previous, provider string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `
UPDATE conversation_branches SET provider_conversation_id = ?
WHERE id = ? AND conversation_id = ? AND provider_conversation_id = ?
  AND NOT EXISTS (SELECT 1 FROM conversation_turns WHERE conversation_id = ? AND branch_id = ? AND state IN (?, ?))`,
		provider, branchID, conversationID, previous, conversationID, branchID,
		domain.TurnStateCompleted, domain.TurnStateRecovered)
	if err != nil {
		return fmt.Errorf("rebind branch %s provider: %w", branchID, err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rebind branch %s provider: %w", branchID, err)
	}
	if rows != 1 {
		return fmt.Errorf("rebind branch %s provider: %d rows", branchID, rows)
	}
	return nil
}
