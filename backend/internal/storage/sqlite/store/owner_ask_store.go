package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// CreateOwnerAsk inserts a new ask with the project's next ref. check runs in
// the same transaction on the project's existing asks (and may fill in the new
// ask), so a dedupe or cap decision cannot race a second creator; an error from
// it aborts the insert.
func (s *Store) CreateOwnerAsk(ctx context.Context, a domain.OwnerAsk, check func(existing []domain.OwnerAsk, a *domain.OwnerAsk) error) (domain.OwnerAsk, error) {
	err := s.inTx(ctx, "insert owner ask", func(q *gen.Queries) error {
		rows, err := q.ListOwnerAsks(ctx, string(a.ProjectID))
		if err != nil {
			return err
		}
		existing := make([]domain.OwnerAsk, 0, len(rows))
		for _, r := range rows {
			e, err := askFromRow(r)
			if err != nil {
				return err
			}
			existing = append(existing, e)
		}
		if check != nil {
			if err := check(existing, &a); err != nil {
				return err
			}
		}
		seq, err := q.NextOwnerAskSeq(ctx, string(a.ProjectID))
		if err != nil {
			return err
		}
		a.Ref = "Q" + strconv.FormatInt(seq, 10)
		opts, err := json.Marshal(a.Options)
		if err != nil {
			return err
		}
		return q.InsertOwnerAsk(ctx, gen.InsertOwnerAskParams{
			ID: a.ID, ProjectID: string(a.ProjectID), Seq: seq, Lane: a.Lane, LaneRef: a.LaneRef, SessionID: string(a.SessionID),
			Text: a.Text, Reason: a.Reason, OptionsJson: string(opts), Recommended: a.Recommended, About: a.About,
			State: string(a.State), ReaskOf: a.ReaskOf, ReaskReason: a.ReaskReason,
			AnchorTurnID: a.AnchorTurnID, AnchorActivityID: a.AnchorActivityID,
			CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
		})
	})
	return a, err
}

// SetOwnerAskAnchor records the inline card an ask has on its asking turn.
func (s *Store) SetOwnerAskAnchor(ctx context.Context, id, turnID, activityID string) error {
	return s.inTx(ctx, "anchor owner ask", func(q *gen.Queries) error {
		return q.SetOwnerAskAnchor(ctx, gen.SetOwnerAskAnchorParams{AnchorTurnID: turnID, AnchorActivityID: activityID, ID: id})
	})
}

// GetOwnerAsk reads an ask by its ref ("Q3", any case).
func (s *Store) GetOwnerAsk(ctx context.Context, project domain.ProjectID, ref string) (domain.OwnerAsk, bool, error) {
	seq, valid := parseAskRef(ref)
	if !valid {
		return domain.OwnerAsk{}, false, nil
	}
	row, err := s.qr.GetOwnerAskByRef(ctx, gen.GetOwnerAskByRefParams{ProjectID: string(project), Seq: seq})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OwnerAsk{}, false, nil
	}
	if err != nil {
		return domain.OwnerAsk{}, false, err
	}
	a, err := askFromRow(row)
	return a, err == nil, err
}

// ListOwnerAsks lists a project's asks, oldest first.
func (s *Store) ListOwnerAsks(ctx context.Context, project domain.ProjectID) ([]domain.OwnerAsk, error) {
	rows, err := s.qr.ListOwnerAsks(ctx, string(project))
	if err != nil {
		return nil, err
	}
	return asksFromRows(rows)
}

// ListOpenOwnerAsks lists a project's open asks, oldest first.
func (s *Store) ListOpenOwnerAsks(ctx context.Context, project domain.ProjectID) ([]domain.OwnerAsk, error) {
	rows, err := s.qr.ListOpenOwnerAsks(ctx, string(project))
	if err != nil {
		return nil, err
	}
	return asksFromRows(rows)
}

// SaveOwnerAskTransition persists an answer or withdrawal made by
// OwnerAsk.Answered/Withdrawn. The update applies only while the stored ask is
// still open, so of two racing answers one wins and the other gets
// domain.ErrAskNotOpen.
func (s *Store) SaveOwnerAskTransition(ctx context.Context, a domain.OwnerAsk) error {
	answer := ""
	if a.Answer != nil {
		b, err := json.Marshal(a.Answer)
		if err != nil {
			return err
		}
		answer = string(b)
	}
	var answeredAt sql.NullTime
	if !a.AnsweredAt.IsZero() {
		answeredAt = sql.NullTime{Time: a.AnsweredAt, Valid: true}
	}
	return s.inTx(ctx, "update owner ask", func(q *gen.Queries) error {
		n, err := q.UpdateOwnerAskIfOpen(ctx, gen.UpdateOwnerAskIfOpenParams{
			State: string(a.State), AnswerJson: answer, WithdrawReason: a.WithdrawReason,
			UpdatedAt: a.UpdatedAt, AnsweredAt: answeredAt, ID: a.ID,
		})
		if err != nil {
			return err
		}
		if n == 0 {
			return domain.ErrAskNotOpen
		}
		return nil
	})
}

func asksFromRows(rows []gen.OwnerAsk) ([]domain.OwnerAsk, error) {
	out := make([]domain.OwnerAsk, 0, len(rows))
	for _, r := range rows {
		a, err := askFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func askFromRow(r gen.OwnerAsk) (domain.OwnerAsk, error) {
	a := domain.OwnerAsk{
		ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), Ref: "Q" + strconv.FormatInt(r.Seq, 10), Lane: r.Lane, LaneRef: r.LaneRef,
		SessionID: domain.SessionID(r.SessionID), Text: r.Text, Reason: r.Reason, Recommended: r.Recommended,
		About: r.About, State: domain.AskState(r.State), ReaskOf: r.ReaskOf, ReaskReason: r.ReaskReason,
		WithdrawReason: r.WithdrawReason, AnchorTurnID: r.AnchorTurnID, AnchorActivityID: r.AnchorActivityID,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.AnsweredAt.Valid {
		a.AnsweredAt = r.AnsweredAt.Time
	}
	if err := json.Unmarshal([]byte(r.OptionsJson), &a.Options); err != nil {
		return a, fmt.Errorf("decode options of ask %s: %w", r.ID, err)
	}
	if r.AnswerJson != "" {
		a.Answer = &domain.AskAnswer{}
		if err := json.Unmarshal([]byte(r.AnswerJson), a.Answer); err != nil {
			return a, fmt.Errorf("decode answer of ask %s: %w", r.ID, err)
		}
	}
	return a, nil
}

// parseAskRef reads "Q3" (any case) into its sequence number.
func parseAskRef(ref string) (int64, bool) {
	seq, err := strconv.ParseInt(strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(ref)), "Q"), 10, 64)
	return seq, err == nil && seq >= 1
}
