package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// MaxStreamedTextChars caps the provider prose stored for one activity.
//
// Reasoning on a long turn is the case this bounds: a model reasoning at high
// effort can emit tens of kilobytes of summary for one item, and this row is
// re-read by every conversation snapshot poll. 32K is well past what a collapsed
// card shows and past what anyone reads; half the command-output cap because
// reasoning has a settled restatement to fall back on and a program's output does
// not.
const MaxStreamedTextChars = 32 * 1024

// AppendActivityStreamedText folds a streamed provider-prose delta into its
// activity.
//
// Reports whether a row was found. A delta for an activity Tallpine has not recorded is
// an ordinary race, not a failure: the provider can emit a reasoning delta before
// the item/started that creates the row, and the settled summary on item/completed
// still lands.
func (s *Store) AppendActivityStreamedText(
	ctx context.Context,
	conversationID, providerItemID, delta string,
	now time.Time,
) (bool, error) {
	if delta == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.AppendConversationActivityStreamedText(ctx,
		gen.AppendConversationActivityStreamedTextParams{
			Delta:          delta,
			MaxTextChars:   MaxStreamedTextChars,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("append streamed text to %s: %w", providerItemID, err)
	}
	if rows > 0 {
		return true, nil
	}
	found, err := q.ConversationActivityExistsForProviderItem(ctx,
		gen.ConversationActivityExistsForProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("find streamed text activity %s after no-op append: %w", providerItemID, err)
	}
	return found, nil
}

// SettleActivityStreamedText replaces streamed prose with the provider's settled
// version, and reports whether the activity was there to update.
//
// Replacing rather than appending is what makes a dropped delta cosmetic: the
// accumulated text is a preview of the settled text, so once the settled form
// arrives it is the only account worth keeping.
func (s *Store) SettleActivityStreamedText(
	ctx context.Context,
	conversationID, providerItemID, text string,
	now time.Time,
) (bool, error) {
	if providerItemID == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.SettleConversationActivityStreamedText(ctx,
		gen.SettleConversationActivityStreamedTextParams{
			StreamedText:   text,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("settle streamed text on %s: %w", providerItemID, err)
	}
	return rows > 0, nil
}

// AppendAssistantDelta folds a streaming delta into its message, creating the
// message on first sight. The provider item id is the correlation key because Tallpine
// does not choose the provider's message identity.
func (s *Store) AppendAssistantDelta(
	ctx context.Context,
	conversationID, providerItemID, providerTurnID, delta, messageID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	_, err := q.SelectConversationMessageByProviderItem(ctx,
		gen.SelectConversationMessageByProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	switch {
	case err == nil:
		if appendErr := q.AppendConversationMessageDelta(ctx,
			gen.AppendConversationMessageDeltaParams{
				Text:           delta,
				UpdatedAt:      now,
				ConversationID: conversationID,
				ProviderItemID: providerItemID,
			}); appendErr != nil {
			return fmt.Errorf("append delta to %s: %w", providerItemID, appendErr)
		}
		return nil

	case errors.Is(err, sql.ErrNoRows):
		var turnID sql.NullString
		streaming := int64(1)
		if providerTurnID != "" {
			turn, turnErr := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID,
				ProviderTurnID: providerTurnID,
			})
			if turnErr == nil {
				turnID = sql.NullString{String: turn.ID, Valid: true}
				if turn.State.Terminal() {
					streaming = 0
				}
			} else if !errors.Is(turnErr, sql.ErrNoRows) {
				return fmt.Errorf("lookup turn %s for assistant delta: %w", providerTurnID, turnErr)
			}
		}
		return s.inTx(ctx, "insert assistant message", func(q *gen.Queries) error {
			sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
				UpdatedAt: now,
				ID:        conversationID,
			})
			if seqErr != nil {
				return fmt.Errorf("allocate sequence: %w", seqErr)
			}
			return q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
				ID:             messageID,
				ConversationID: conversationID,
				TurnID:         turnID,
				Sequence:       sequence,
				Role:           domain.MessageRoleAssistant,
				Origin:         domain.MessageOriginProvider,
				Text:           delta,
				Streaming:      streaming,
				ProviderItemID: providerItemID,
				CreatedAt:      now,
				UpdatedAt:      now,
			})
		})

	default:
		return fmt.Errorf("lookup message %s: %w", providerItemID, err)
	}
}

// SettleAssistantMessage replaces streamed text with the provider's settled text
// and stops the streaming marker. An empty settle means the provider settled
// nothing, so it keeps the streamed text.
func (s *Store) SettleAssistantMessage(
	ctx context.Context,
	conversationID, providerItemID, providerTurnID, text, messageID string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	existing, err := q.SelectConversationMessageByProviderItem(ctx,
		gen.SelectConversationMessageByProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		// A message whose deltas Tallpine never saw — possible if it completed inside a
		// reconnect window. Record it whole rather than dropping it.
		turnID := s.turnIDFor(ctx, conversationID, providerTurnID)
		return s.inTx(ctx, "insert assistant message", func(q *gen.Queries) error {
			sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
				UpdatedAt: now,
				ID:        conversationID,
			})
			if seqErr != nil {
				return fmt.Errorf("allocate sequence: %w", seqErr)
			}
			return q.InsertConversationMessage(ctx, gen.InsertConversationMessageParams{
				ID:             messageID,
				ConversationID: conversationID,
				TurnID:         turnID,
				Sequence:       sequence,
				Role:           domain.MessageRoleAssistant,
				Origin:         domain.MessageOriginProvider,
				Text:           text,
				ProviderItemID: providerItemID,
				CreatedAt:      now,
				UpdatedAt:      now,
			})
		})
	}
	if err != nil {
		return fmt.Errorf("lookup message %s: %w", providerItemID, err)
	}
	if text == "" {
		text = existing.Text
	}

	if err := q.SettleConversationMessage(ctx, gen.SettleConversationMessageParams{
		Text:           text,
		UpdatedAt:      now,
		ConversationID: conversationID,
		ProviderItemID: providerItemID,
	}); err != nil {
		return fmt.Errorf("settle message %s: %w", providerItemID, err)
	}
	return nil
}

// ActivityStatusByProviderItem reports the status of one provider item.
// Missing rows return ok=false.
func (s *Store) ActivityStatusByProviderItem(ctx context.Context, conversationID, providerItemID string) (domain.ActivityStatus, bool, error) {
	row, err := s.qr.SelectConversationActivityByProviderItem(ctx, gen.SelectConversationActivityByProviderItemParams{
		ConversationID: conversationID,
		ProviderItemID: providerItemID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Status, true, nil
}

func tallpineActionStatusSettled(status domain.ActivityStatus) bool {
	switch status {
	case domain.ActivityStatusCompleted, domain.ActivityStatusFailed, domain.ActivityStatusCancelled, domain.ActivityStatusResolved:
		return true
	default:
		return false
	}
}

// UpsertActivity records or updates a non-message timeline entry.
// A running ao_action does not replace a completed, failed, cancelled, or
// resolved row. The admission write and the terminal callback take this lock
// separately, so the decision uses the row read in this call.
func (s *Store) UpsertActivity(
	ctx context.Context,
	conversationID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	detail := string(activity.Detail)

	if activity.ProviderItemID != "" {
		row, err := q.SelectConversationActivityByProviderItem(ctx,
			gen.SelectConversationActivityByProviderItemParams{
				ConversationID: conversationID,
				ProviderItemID: activity.ProviderItemID,
			})
		if err == nil {
			if activity.Kind == domain.ActivityKindTallpineAction &&
				activity.Status == domain.ActivityStatusRunning &&
				tallpineActionStatusSettled(row.Status) {
				return nil
			}
			if s.coalesceActivityWrites.Load() && row.Status == activity.Status &&
				row.Summary == activity.Summary && row.DetailJson == detail {
				// A provider heartbeat re-sends the item unchanged. Rewriting it
				// would only bump the revision, and every revision is a change_log
				// row and a refetch in every client.
				return nil
			}
			if settleErr := q.SettleConversationActivity(ctx,
				gen.SettleConversationActivityParams{
					Status:         activity.Status,
					Summary:        activity.Summary,
					DetailJson:     detail,
					UpdatedAt:      now,
					ConversationID: conversationID,
					ProviderItemID: activity.ProviderItemID,
				}); settleErr != nil {
				return fmt.Errorf("settle activity %s: %w", activity.ProviderItemID, settleErr)
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("lookup activity %s: %w", activity.ProviderItemID, err)
		}
	}

	turnID := s.turnIDFor(ctx, conversationID, providerTurnID)
	return s.inTx(ctx, "insert activity", func(q *gen.Queries) error {
		sequence, seqErr := q.NextConversationSequence(ctx, gen.NextConversationSequenceParams{
			UpdatedAt: now,
			ID:        conversationID,
		})
		if seqErr != nil {
			return fmt.Errorf("allocate sequence: %w", seqErr)
		}
		if err := recordSteerInteraction(ctx, q, conversationID, activity, now); err != nil {
			return err
		}
		return q.InsertConversationActivity(ctx, gen.InsertConversationActivityParams{
			ID:             activity.ID,
			ConversationID: conversationID,
			TurnID:         turnID,
			Sequence:       sequence,
			Kind:           activity.Kind,
			Status:         activity.Status,
			Summary:        activity.Summary,
			DetailJson:     detail,
			RequestID:      activity.RequestID,
			ProviderItemID: activity.ProviderItemID,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	})
}

// MaxCommandOutputChars caps the streamed output stored for one command.
//
// A command that prints without bound (a build, a test run with verbose logging, a
// `yes`) must not put megabytes into a row that every conversation snapshot reads.
// 64K is comfortably more than a person reads in a timeline card and is where the
// full record belongs to a shell in the worktree instead.
//
// Exported so the truncation boundary is testable and so a caller can state the
// limit rather than restating a magic number.
const MaxCommandOutputChars = 64 * 1024

// AppendCommandOutput folds a streamed output delta into its activity.
//
// Reports whether a row was found. A delta for an activity Tallpine has not recorded is
// an ordinary race, not a failure: the provider can emit a delta before the
// item/started that creates the row, and the aggregate on item/completed still
// lands. Silently dropping it is correct; claiming an error is not.
func (s *Store) AppendCommandOutput(
	ctx context.Context,
	conversationID, providerItemID, delta string,
	now time.Time,
) (bool, error) {
	if delta == "" {
		return false, nil
	}
	q, unlock := s.conversationWriter(ctx)
	defer unlock()

	rows, err := q.AppendConversationActivityOutput(ctx,
		gen.AppendConversationActivityOutputParams{
			Delta:          delta,
			MaxOutputChars: MaxCommandOutputChars,
			UpdatedAt:      now,
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("append command output to %s: %w", providerItemID, err)
	}
	if rows > 0 {
		return true, nil
	}
	found, err := q.ConversationActivityExistsForProviderItem(ctx,
		gen.ConversationActivityExistsForProviderItemParams{
			ConversationID: conversationID,
			ProviderItemID: providerItemID,
		})
	if err != nil {
		return false, fmt.Errorf("find command output activity %s after no-op append: %w", providerItemID, err)
	}
	return found, nil
}

// SetTurnDiff overwrites a turn's changed-file summary.
//
// Reports whether the turn was found. A diff for a turn Tallpine never recorded happens
// after a restart, when a controller reattaches to a provider turn that predates
// it, and there is nothing to update.
func (s *Store) SetTurnDiff(
	ctx context.Context,
	conversationID, providerTurnID string,
	diff domain.ConversationTurnDiff,
	now time.Time,
) (bool, error) {
	if providerTurnID == "" {
		// Without a turn id there is no row to attribute this to, and guessing the
		// most recent turn would attach one turn's edits to another.
		return false, nil
	}
	encoded, err := json.Marshal(diff)
	if err != nil {
		return false, fmt.Errorf("encode turn diff for %s: %w", providerTurnID, err)
	}

	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	rows, err := q.UpdateConversationTurnDiff(ctx, gen.UpdateConversationTurnDiffParams{
		DiffJson:       string(encoded),
		ConversationID: conversationID,
		ProviderTurnID: providerTurnID,
	})
	if err != nil {
		return false, fmt.Errorf("set turn diff for %s: %w", providerTurnID, err)
	}
	return rows > 0, nil
}

// ResolveApproval marks an approval answered. It only matches a still-pending
// row, so a card the user left on screen cannot answer a newer request.
func (s *Store) ResolveApproval(
	ctx context.Context,
	conversationID, requestID, detailJSON string,
	now time.Time,
) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.ResolveConversationApproval(ctx, gen.ResolveConversationApprovalParams{
		DetailJson:     detailJSON,
		UpdatedAt:      now,
		ConversationID: conversationID,
		RequestID:      requestID,
	}); err != nil {
		return fmt.Errorf("resolve approval %s: %w", requestID, err)
	}
	return nil
}

// HasPendingConversationInteractions reports whether the durable conversation
// still contains an actionable approval or structured-input request.
func (s *Store) HasPendingConversationInteractions(ctx context.Context, conversationID string) (bool, error) {
	pending, err := s.qr.HasPendingConversationInteractions(ctx, conversationID)
	if err != nil {
		return false, fmt.Errorf("check pending interactions for %s: %w", conversationID, err)
	}
	return pending, nil
}

// FailPendingApprovals closes out anything the user can no longer answer, because
// the provider call it was blocking is gone.
func (s *Store) FailPendingApprovals(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailPendingConversationApprovals(ctx,
		gen.FailPendingConversationApprovalsParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
		}); err != nil {
		return fmt.Errorf("fail pending approvals for %s: %w", conversationID, err)
	}
	return nil
}

// FailPendingInputs closes structured input requests after the provider call
// waiting for them has gone away. Keeping this separate from approvals preserves
// the activity-kind boundary even though both use the same pending/resolved state
// machine.
func (s *Store) FailPendingInputs(ctx context.Context, conversationID string, now time.Time) error {
	q, unlock := s.conversationWriter(ctx)
	defer unlock()
	if err := q.FailPendingConversationInputs(ctx,
		gen.FailPendingConversationInputsParams{
			UpdatedAt:      now,
			ConversationID: conversationID,
		}); err != nil {
		return fmt.Errorf("fail pending inputs for %s: %w", conversationID, err)
	}
	return nil
}
