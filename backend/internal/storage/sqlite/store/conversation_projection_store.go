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

// ProjectProviderEvent commits a raw provider event and the durable projection
// derived from it together. A real provider event identity deduplicates the
// entire unit; an empty identity is deliberately never deduplicated.
func (s *Store) ProjectProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	return s.projectProviderEvent(ctx, conversationID, session, "", generation, providerEventID, method, payloadJSON, now, project)
}

// ProjectReviewProviderEvent uses the review's controller generation. Reviewer
// Chat shares the worker session but never owns its Chat controller fence.
func (s *Store) ProjectReviewProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	return s.projectProviderEvent(ctx, conversationID, session, reviewID, generation, providerEventID, method, payloadJSON, now, project)
}

func (s *Store) projectProviderEvent(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if q, ok := ctx.Value(conversationProjectionTxKey{}).(*gen.Queries); ok && q != nil {
		return projectProviderEventTx(
			ctx, q, conversationID, session, reviewID, generation,
			providerEventID, method, payloadJSON, now, project,
		)
	}
	return s.groupCommitProjection(ctx, func(ctx context.Context, _ *sql.Tx, q *gen.Queries) (bool, error) {
		return projectProviderEventTx(
			ctx, q, conversationID, session, reviewID, generation,
			providerEventID, method, payloadJSON, now, project,
		)
	})
}

func projectProviderEventTx(
	ctx context.Context,
	q *gen.Queries,
	conversationID string,
	session domain.SessionID,
	reviewID, generation, providerEventID, method, payloadJSON string,
	now time.Time,
	project func(context.Context) error,
) (bool, error) {
	if reviewID != "" {
		owner, err := q.GetReviewByID(ctx, reviewID)
		if err != nil {
			return false, fmt.Errorf("read reviewer controller generation for %s: %w", reviewID, err)
		}
		if owner.ControllerGeneration != generation || owner.InterfaceMode != "chat" || owner.SessionID != session {
			return false, nil
		}
	} else {
		owner, err := q.GetSessionControllerGeneration(ctx, session)
		if err != nil {
			return false, fmt.Errorf("read controller generation for %s: %w", session, err)
		}
		if owner != generation {
			return false, nil
		}
	}
	inserted, err := q.InsertConversationProviderEvent(ctx, gen.InsertConversationProviderEventParams{
		ConversationID:  conversationID,
		SessionID:       session,
		ProviderEventID: providerEventID,
		Method:          method,
		PayloadJson:     payloadJSON,
		ReceivedAt:      now,
	})
	if err != nil {
		return false, fmt.Errorf("archive provider event %s: %w", method, err)
	}
	if inserted == 0 {
		return false, nil
	}
	txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
	if err := project(txCtx); err != nil {
		return false, fmt.Errorf("project provider event %s: %w", method, err)
	}
	return true, nil
}

// SetProviderTitle records the title the provider reports for this conversation.
func (s *Store) SetProviderTitle(
	ctx context.Context,
	conversationID, title string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.UpdateConversationProviderTitle(ctx,
		gen.UpdateConversationProviderTitleParams{
			ProviderTitle: title,
			UpdatedAt:     now,
			ID:            conversationID,
		}); err != nil {
		return fmt.Errorf("set provider title for %s: %w", conversationID, err)
	}
	return nil
}

// ApplyProviderTitle pushes a provider thread title into the session's display
// name, and reports whether it landed.
//
// applied=false is the ordinary outcome, not a failure: it means the session
// already carries a name a person chose, and that name wins. The check is the
// UPDATE's own guard rather than a read followed by a write, because a manual
// rename arriving between those two would be silently overwritten by whatever the
// provider said a moment earlier.
//
// Writing display_name is what fans the new label out to every live surface: the
// sessions_cdc_update trigger includes display_name in its guard, so the existing
// session_updated event carries it without a second notification path.
func (s *Store) ApplyProviderTitle(
	ctx context.Context,
	conversationID string,
	session domain.SessionID,
	title string,
	now time.Time,
) (bool, error) {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	conversation, err := q.SelectConversationByID(ctx, conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrConversationNotFound
	}
	if err != nil {
		return false, fmt.Errorf("select conversation %s: %w", conversationID, err)
	}

	var applied bool
	err = s.inTx(ctx, "apply provider title", func(q *gen.Queries) error {
		rows, updateErr := q.ApplyConversationTitleToSession(ctx,
			gen.ApplyConversationTitleToSessionParams{
				DisplayName: title,
				UpdatedAt:   now,
				ID:          session,
				// The witness: only a label Tallpine itself last wrote may be replaced.
				DisplayName_2: conversation.AppliedTitle,
			})
		if updateErr != nil {
			return fmt.Errorf("apply title to session %s: %w", session, updateErr)
		}
		if rows == 0 {
			return nil
		}
		applied = true
		return q.UpdateConversationAppliedTitle(ctx, gen.UpdateConversationAppliedTitleParams{
			AppliedTitle: title,
			UpdatedAt:    now,
			ID:           conversationID,
		})
	})
	if err != nil {
		return false, err
	}
	return applied, nil
}

// ProviderEventsSince reads the raw provider-event archive forward from an id.
// This is the recovery path: when a projection turns out to be wrong, these rows
// are the only account of what the provider actually said.
func (s *Store) ProviderEventsSince(
	ctx context.Context,
	conversationID string,
	afterID int64,
	limit int64,
) ([]gen.ConversationProviderEvent, error) {
	rows, err := s.qr.SelectConversationProviderEvents(ctx,
		gen.SelectConversationProviderEventsParams{
			ConversationID: conversationID,
			ID:             afterID,
			PageLimit:      limit,
		})
	if err != nil {
		return nil, fmt.Errorf("select provider events for %s: %w", conversationID, err)
	}
	return rows, nil
}
