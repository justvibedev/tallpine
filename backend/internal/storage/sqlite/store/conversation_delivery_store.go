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

// ErrQueuedEditDeliveryConflict refuses reuse of a key for a different mutation.
var ErrQueuedEditDeliveryConflict = ports.ErrQueuedEditDeliveryConflict

// QueuedEditDelivery finds the fingerprint of an atomically accepted queue edit.
func (s *Store) QueuedEditDelivery(ctx context.Context, conversationID, clientMessageID string) (string, bool, error) {
	hash, err := s.conversationReader(ctx).SelectConversationQueuedEditDelivery(ctx,
		gen.SelectConversationQueuedEditDeliveryParams{ConversationID: conversationID, ClientMessageID: clientMessageID})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return hash, err == nil, err
}

func reserveConversationDelivery[Row, Delivery any](
	ctx context.Context,
	s *Store,
	insert func(context.Context) (int64, error),
	load func(context.Context) (Row, error),
	toDomain func(Row) Delivery,
	insertError, loadError string,
) (delivery Delivery, created bool, err error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := insert(ctx)
	if err != nil {
		return delivery, false, fmt.Errorf("%s: %w", insertError, err)
	}
	row, err := load(ctx)
	if err != nil {
		return delivery, false, fmt.Errorf("%s: %w", loadError, err)
	}
	return toDomain(row), rows == 1, nil
}

// EditDelivery loads the durable outcome for one caller-owned inline edit key.
func (s *Store) EditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationEditDelivery, bool, error) {
	row, err := s.conversationReader(ctx).SelectConversationEditDelivery(ctx,
		gen.SelectConversationEditDeliveryParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationEditDelivery{}, false, nil
	}
	if err != nil {
		return domain.ConversationEditDelivery{}, false,
			fmt.Errorf("select edit delivery %s: %w", clientMessageID, err)
	}
	return editDeliveryToDomain(row), true, nil
}

// ReserveEditDelivery claims a client handle before anchor lookup or provider
// I/O. created is false when another request already owns the handle.
func (s *Store) ReserveEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID, requestJSON string,
	now time.Time,
) (delivery domain.ConversationEditDelivery, created bool, err error) {
	return reserveConversationDelivery(
		ctx,
		s,
		func(ctx context.Context) (int64, error) {
			return s.qw.InsertConversationEditDeliveryReservation(ctx,
				gen.InsertConversationEditDeliveryReservationParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
					RequestJson: requestJSON, CreatedAt: now,
				})
		},
		func(ctx context.Context) (gen.ConversationEditDelivery, error) {
			return s.qw.SelectConversationEditDelivery(ctx,
				gen.SelectConversationEditDeliveryParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
				})
		},
		editDeliveryToDomain,
		"reserve edit delivery "+clientMessageID,
		"read edit delivery reservation "+clientMessageID,
	)
}

// RecoverCompletedEditDelivery uses provider completion as proof of acceptance.
// A queued, bound, or failed turn alone does not prove provider execution.
func (s *Store) RecoverCompletedEditDelivery(ctx context.Context, conversationID, clientMessageID string, now time.Time) error {
	row, err := s.conversationReader(ctx).SelectCompletedEditReplacement(ctx, gen.SelectCompletedEditReplacementParams{
		ConversationID: conversationID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load completed edit replacement: %w", err)
	}
	turn := turnToDomain(row.ConversationTurn)
	return s.CompleteEditDelivery(ctx, conversationID, clientMessageID, row.ParentBranchID.String, turn.BranchID, turn, now)
}

// BeginEditProviderWork fences retries before any provider operation can occur.
func (s *Store) BeginEditProviderWork(ctx context.Context, conversationID, clientMessageID, generation string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.BeginConversationEditProviderWork(ctx, gen.BeginConversationEditProviderWorkParams{
		ConversationID: conversationID, ClientMessageID: clientMessageID, Generation: generation,
	})
	if err != nil {
		return fmt.Errorf("begin edit provider work: %w", err)
	}
	if rows != 1 {
		return errors.New("edit provider work already started or controller replaced")
	}
	return nil
}

// CompleteEditDelivery attaches the replacement turn to its branch and records
// the replayable accepted result in one transaction. A crash cannot publish one
// fact without the other.
func (s *Store) CompleteEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID, sourceBranchID, activeBranchID string,
	turn domain.ConversationTurn,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete edit delivery", func(q *gen.Queries) error {
		rows, err := q.UpdateConversationBranchReplacement(ctx,
			gen.UpdateConversationBranchReplacementParams{
				ReplacementTurnID: nullableString(turn.ID), BranchID: activeBranchID,
			})
		if err != nil {
			return fmt.Errorf("update conversation branch %s replacement: %w", activeBranchID, err)
		}
		if rows != 1 {
			return fmt.Errorf("%w: %s", domain.ErrNoConversationBranch, activeBranchID)
		}
		rows, err = q.AcceptConversationEditDelivery(ctx,
			gen.AcceptConversationEditDeliveryParams{
				SourceBranchID: sourceBranchID, ActiveBranchID: activeBranchID,
				TurnID: turn.ID, HandledBySessionID: string(turn.HandledBySessionID),
				ProviderTurnID: turn.ProviderTurnID, TurnState: string(turn.State),
				TurnRequestedAt: sql.NullTime{Time: turn.RequestedAt, Valid: !turn.RequestedAt.IsZero()},
				SettledAt:       sql.NullTime{Time: now, Valid: true},
				ConversationID:  conversationID, ClientMessageID: clientMessageID,
			})
		if err != nil {
			return fmt.Errorf("accept edit delivery %s: %w", clientMessageID, err)
		}
		if rows != 1 {
			return fmt.Errorf("accept edit delivery %s: reservation is not active", clientMessageID)
		}
		return nil
	})
}

// RejectEditDelivery settles a definitive edit non-acceptance for replay after
// active lineage changes or daemon restart.
func (s *Store) RejectEditDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	kind domain.ConversationEditRejectionKind,
	message string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RejectConversationEditDelivery(ctx,
		gen.RejectConversationEditDeliveryParams{
			RejectionKind: string(kind), RejectionMessage: message,
			SettledAt:      sql.NullTime{Time: now, Valid: true},
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if err != nil {
		return fmt.Errorf("reject edit delivery %s: %w", clientMessageID, err)
	}
	if rows != 1 {
		return fmt.Errorf("reject edit delivery %s: reservation is not active", clientMessageID)
	}
	return nil
}

func editDeliveryToDomain(row gen.ConversationEditDelivery) domain.ConversationEditDelivery {
	delivery := domain.ConversationEditDelivery{
		ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		RequestJSON: row.RequestJson, State: domain.ConversationEditDeliveryState(row.State),
		ProviderWorkStarted: row.ProviderWorkStarted != 0,
		SourceBranchID:      row.SourceBranchID, ActiveBranchID: row.ActiveBranchID,
		Turn: domain.ConversationTurn{
			ID: row.TurnID, ConversationID: row.ConversationID,
			HandledBySessionID: domain.SessionID(row.HandledBySessionID),
			ProviderTurnID:     row.ProviderTurnID, State: domain.TurnState(row.TurnState),
		},
		RejectionKind:    domain.ConversationEditRejectionKind(row.RejectionKind),
		RejectionMessage: row.RejectionMessage, CreatedAt: row.CreatedAt,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		delivery.SettledAt = &settled
	}
	if row.TurnRequestedAt.Valid {
		delivery.Turn.RequestedAt = row.TurnRequestedAt.Time
	}
	return delivery
}

// SteerDelivery loads the durable outcome for one caller-owned idempotency key.
func (s *Store) SteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
) (domain.ConversationSteerDelivery, bool, error) {
	row, err := s.conversationReader(ctx).SelectConversationSteerDelivery(ctx,
		gen.SelectConversationSteerDeliveryParams{
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ConversationSteerDelivery{}, false, nil
	}
	if err != nil {
		return domain.ConversationSteerDelivery{}, false,
			fmt.Errorf("select steer delivery %s: %w", clientMessageID, err)
	}
	return steerDeliveryToDomain(row), true, nil
}

// ReserveSteerDelivery claims a client handle before provider I/O. created is
// false when another controller or a previous process already owns the handle;
// callers must interpret the returned durable state instead of dispatching.
func (s *Store) ReserveSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID, requestJSON string,
	now time.Time,
) (delivery domain.ConversationSteerDelivery, created bool, err error) {
	return reserveConversationDelivery(
		ctx,
		s,
		func(ctx context.Context) (int64, error) {
			return s.qw.InsertConversationSteerDeliveryReservation(ctx,
				gen.InsertConversationSteerDeliveryReservationParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
					RequestJson: requestJSON, CreatedAt: now,
				})
		},
		func(ctx context.Context) (gen.ConversationSteerDelivery, error) {
			return s.qw.SelectConversationSteerDelivery(ctx,
				gen.SelectConversationSteerDeliveryParams{
					ConversationID: conversationID, ClientMessageID: clientMessageID,
				})
		},
		steerDeliveryToDomain,
		"reserve steer delivery "+clientMessageID,
		"load reserved steer delivery "+clientMessageID,
	)
}

// CompleteSteerDelivery records the visible timeline row and accepted provider
// result together. A crash can therefore leave either an accepted replay or an
// unresolved reservation, never an accepted result without its visible cause.
func (s *Store) CompleteSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID, providerTurnID string,
	activity domain.ConversationActivity,
	now time.Time,
) error {
	return s.CompleteSteerBatchDelivery(ctx, conversationID, clientMessageID, providerTurnID, activity, nil, now)
}

// CompleteSteerBatchDelivery records the steer receipt and all source links together.
func (s *Store) CompleteSteerBatchDelivery(
	ctx context.Context,
	conversationID, clientMessageID, providerTurnID string,
	activity domain.ConversationActivity,
	turnIDs []string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "complete steer delivery", func(q *gen.Queries) error {
		if len(turnIDs) > 0 {
			target, err := q.SelectConversationTurnByProviderID(ctx, gen.SelectConversationTurnByProviderIDParams{
				ConversationID: conversationID, ProviderTurnID: providerTurnID,
			})
			if err != nil {
				return err
			}
			for _, id := range turnIDs {
				changed, err := q.CompleteQueuedConversationBatchMember(ctx, gen.CompleteQueuedConversationBatchMemberParams{
					CompletedAt:      sql.NullTime{Time: now, Valid: true},
					PromotedToTurnID: sql.NullString{String: target.ID, Valid: true},
					DeliveryMode:     domain.DeliveryModeSteered,
					ID:               id, ConversationID: conversationID,
				})
				if checkErr := expectBatchRow("complete steered batch turn", id, changed, err); checkErr != nil {
					return checkErr
				}
			}
		}
		txCtx := context.WithValue(ctx, conversationProjectionTxKey{}, q)
		if err := s.UpsertActivity(txCtx, conversationID, providerTurnID, activity, now); err != nil {
			return err
		}
		rows, err := q.AcceptConversationSteerDelivery(ctx,
			gen.AcceptConversationSteerDeliveryParams{
				ProviderTurnID: providerTurnID, ActivityID: activity.ID,
				SettledAt:      sql.NullTime{Time: now, Valid: true},
				ConversationID: conversationID, ClientMessageID: clientMessageID,
			})
		if err != nil {
			return fmt.Errorf("accept steer delivery %s: %w", clientMessageID, err)
		}
		if rows != 1 {
			return fmt.Errorf("accept steer delivery %s: reservation is not active", clientMessageID)
		}
		return nil
	})
}

// RejectSteerDelivery settles a provider-declared non-acceptance. Retrying the
// same handle replays this typed result without contacting the provider again.
func (s *Store) RejectSteerDelivery(
	ctx context.Context,
	conversationID, clientMessageID string,
	kind domain.ConversationSteerRejectionKind,
	message string,
	now time.Time,
) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.RejectConversationSteerDelivery(ctx,
		gen.RejectConversationSteerDeliveryParams{
			RejectionKind: string(kind), RejectionMessage: message,
			SettledAt:      sql.NullTime{Time: now, Valid: true},
			ConversationID: conversationID, ClientMessageID: clientMessageID,
		})
	if err != nil {
		return fmt.Errorf("reject steer delivery %s: %w", clientMessageID, err)
	}
	if rows != 1 {
		return fmt.Errorf("reject steer delivery %s: reservation is not active", clientMessageID)
	}
	return nil
}

func steerDeliveryToDomain(row gen.ConversationSteerDelivery) domain.ConversationSteerDelivery {
	delivery := domain.ConversationSteerDelivery{
		ConversationID: row.ConversationID, ClientMessageID: row.ClientMessageID,
		RequestJSON: row.RequestJson, State: domain.ConversationSteerDeliveryState(row.State),
		ProviderTurnID: row.ProviderTurnID, ActivityID: row.ActivityID,
		RejectionKind:    domain.ConversationSteerRejectionKind(row.RejectionKind),
		RejectionMessage: row.RejectionMessage, CreatedAt: row.CreatedAt,
	}
	if row.SettledAt.Valid {
		settled := row.SettledAt.Time
		delivery.SettledAt = &settled
	}
	return delivery
}
