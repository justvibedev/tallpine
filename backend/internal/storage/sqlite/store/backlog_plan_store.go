package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// CreateBacklogPlan inserts a plan in its starting status. When the item
// already has an open plan the unique index refuses the insert and the open
// plan is returned with created false.
func (s *Store) CreateBacklogPlan(ctx context.Context, p domain.BacklogPlan) (domain.BacklogPlan, bool, error) {
	err := s.inTx(ctx, "insert backlog plan", func(q *gen.Queries) error {
		return q.InsertBacklogPlan(ctx, gen.InsertBacklogPlanParams{
			ID: p.ID, ProjectID: string(p.ProjectID), ItemID: p.ItemID, Status: string(p.Status),
			PlannerSessionID: string(p.PlannerSessionID), BaseCommit: p.BaseCommit, BaseBranch: p.BaseBranch,
			ItemUpdatedAtStart: p.ItemUpdatedAt, StartedAt: p.StartedAt, UpdatedAt: p.UpdatedAt,
			NotifySessionID: string(p.NotifySessionID),
		})
	})
	if err == nil {
		return p, true, nil
	}
	if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return domain.BacklogPlan{}, false, err
	}
	open, ok, err := s.OpenBacklogPlanForItem(ctx, p.ItemID)
	if err != nil {
		return domain.BacklogPlan{}, false, err
	}
	if !ok {
		return domain.BacklogPlan{}, false, fmt.Errorf("insert backlog plan: conflict without an open plan")
	}
	return open, false, nil
}

// GetBacklogPlan reads one plan by id.
func (s *Store) GetBacklogPlan(ctx context.Context, id string) (domain.BacklogPlan, bool, error) {
	return planResult(s.qr.SelectBacklogPlan(ctx, id))
}

// OpenBacklogPlanForItem returns the item's open plan, if any.
func (s *Store) OpenBacklogPlanForItem(ctx context.Context, itemID string) (domain.BacklogPlan, bool, error) {
	return planResult(s.qr.SelectOpenBacklogPlanForItem(ctx, itemID))
}

// CurrentBacklogPlanForItem returns the item's newest plan that was not
// superseded: the open plan, else the approved or failed one.
func (s *Store) CurrentBacklogPlanForItem(ctx context.Context, itemID string) (domain.BacklogPlan, bool, error) {
	return planResult(s.qr.SelectCurrentBacklogPlanForItem(ctx, itemID))
}

// OpenBacklogPlansForProject lists the project's plans that still wait on
// someone: open ones and failed ones, oldest first.
func (s *Store) OpenBacklogPlansForProject(ctx context.Context, projectID domain.ProjectID) ([]domain.BacklogPlan, error) {
	rows, err := s.qr.SelectOpenBacklogPlansForProject(ctx, string(projectID))
	if err != nil {
		return nil, err
	}
	out := make([]domain.BacklogPlan, 0, len(rows))
	for _, r := range rows {
		p, err := planFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// SupersededBacklogPlansForItem lists the item's superseded plans, newest first.
func (s *Store) SupersededBacklogPlansForItem(ctx context.Context, itemID string) ([]domain.BacklogPlan, error) {
	rows, err := s.qr.SelectSupersededBacklogPlansForItem(ctx, itemID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.BacklogPlan, 0, len(rows))
	for _, r := range rows {
		p, err := planFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// BacklogPlanByPlanner returns the newest plan a planner session is recorded on.
func (s *Store) BacklogPlanByPlanner(ctx context.Context, sessionID domain.SessionID) (domain.BacklogPlan, bool, error) {
	return planResult(s.qr.SelectBacklogPlanByPlanner(ctx, string(sessionID)))
}

// UpdateBacklogPlan rewrites a plan under a mutation function. The mutate
// callback sees the stored plan and may return an error to abort.
func (s *Store) UpdateBacklogPlan(ctx context.Context, id string, mutate func(*domain.BacklogPlan) error) (domain.BacklogPlan, error) {
	var out domain.BacklogPlan
	err := s.inTx(ctx, "update backlog plan", func(q *gen.Queries) error {
		row, err := q.SelectBacklogPlan(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("backlog plan %s not found", id)
		}
		if err != nil {
			return err
		}
		p, err := planFromRow(row)
		if err != nil {
			return err
		}
		if err := mutate(&p); err != nil {
			return err
		}
		out = p
		return writePlan(ctx, q, p)
	})
	return out, err
}

// PlanMessageSource describes a chat message used as proof of an owner decision.
// Human is true only for a message the owner typed: an agent's `tallpine send`
// is stored with origin human too, but carries a sender session id.
type PlanMessageSource struct {
	Human     bool
	ProjectID domain.ProjectID
	At        time.Time
}

// BacklogPlanMessageSource looks up a conversation message.
func (s *Store) BacklogPlanMessageSource(ctx context.Context, messageID string) (PlanMessageSource, bool, error) {
	row, err := s.qr.SelectConversationMessageSource(ctx, messageID)
	if errors.Is(err, sql.ErrNoRows) {
		return PlanMessageSource{}, false, nil
	}
	if err != nil {
		return PlanMessageSource{}, false, err
	}
	src := PlanMessageSource{Human: row.Role == domain.MessageRoleUser && row.Origin == domain.MessageOriginHuman && row.SenderSessionID == "", At: row.CreatedAt}
	if row.ProjectID != nil {
		src.ProjectID = *row.ProjectID
	}
	return src, true, nil
}

// LatestHumanProjectMessage returns the newest message the owner typed into the
// project's orchestrator conversation.
func (s *Store) LatestHumanProjectMessage(ctx context.Context, projectID domain.ProjectID) (string, time.Time, bool, error) {
	row, err := s.qr.SelectLatestHumanProjectMessage(ctx, &projectID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return row.ID, row.CreatedAt, true, nil
}

func planResult(row gen.BacklogPlan, err error) (domain.BacklogPlan, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogPlan{}, false, nil
	}
	if err != nil {
		return domain.BacklogPlan{}, false, err
	}
	p, err := planFromRow(row)
	return p, err == nil, err
}

func planFromRow(r gen.BacklogPlan) (domain.BacklogPlan, error) {
	p := domain.BacklogPlan{
		ID: r.ID, ProjectID: domain.ProjectID(r.ProjectID), ItemID: r.ItemID, Status: domain.PlanStatus(r.Status),
		PlannerSessionID: domain.SessionID(r.PlannerSessionID), BaseCommit: r.BaseCommit, BaseBranch: r.BaseBranch,
		PlanMarkdown: r.PlanMarkdown, DecisionsMD: r.DecisionsMarkdown, ApprovedBy: r.ApprovedBy,
		OwnerMessageID: r.OwnerMessageID, BuildAnyway: r.BuildAnyway != 0, ItemUpdatedAt: r.ItemUpdatedAtStart,
		StartedAt: r.StartedAt, UpdatedAt: r.UpdatedAt, Failure: r.Failure,
		NotifySessionID: domain.SessionID(r.NotifySessionID),
	}
	if r.ApprovedAt.Valid {
		p.ApprovedAt = r.ApprovedAt.Time
	}
	if r.ItemChangedAt.Valid {
		p.ItemChangedAt = r.ItemChangedAt.Time
	}
	for _, d := range []struct {
		raw string
		dst any
	}{{r.QuestionsJson, &p.Questions}, {r.AssumptionsJson, &p.Assumptions}, {r.PRSplitJson, &p.PRSplit}, {r.ReviewFocusJson, &p.ReviewFocus}} {
		if strings.TrimSpace(d.raw) == "" {
			continue
		}
		if err := json.Unmarshal([]byte(d.raw), d.dst); err != nil {
			return domain.BacklogPlan{}, fmt.Errorf("decode backlog plan %s: %w", r.ID, err)
		}
	}
	if strings.TrimSpace(r.AnswersJson) != "" {
		var a domain.PlanAnswers
		if err := json.Unmarshal([]byte(r.AnswersJson), &a); err != nil {
			return domain.BacklogPlan{}, fmt.Errorf("decode backlog plan %s answers: %w", r.ID, err)
		}
		p.Answers = &a
	}
	return p, nil
}

func writePlan(ctx context.Context, q *gen.Queries, p domain.BacklogPlan) error {
	enc := func(v any) (string, error) {
		b, err := json.Marshal(v)
		return string(b), err
	}
	var (
		questions, assumptions, split, focus, answers string
		err                                           error
	)
	if questions, err = enc(orEmpty(p.Questions)); err != nil {
		return err
	}
	if assumptions, err = enc(orEmpty(p.Assumptions)); err != nil {
		return err
	}
	if split, err = enc(orEmpty(p.PRSplit)); err != nil {
		return err
	}
	if focus, err = enc(orEmpty(p.ReviewFocus)); err != nil {
		return err
	}
	if p.Answers != nil {
		if answers, err = enc(p.Answers); err != nil {
			return err
		}
	}
	approved := sql.NullTime{Time: p.ApprovedAt, Valid: !p.ApprovedAt.IsZero()}
	changed := sql.NullTime{Time: p.ItemChangedAt, Valid: !p.ItemChangedAt.IsZero()}
	build := int64(0)
	if p.BuildAnyway {
		build = 1
	}
	return q.UpdateBacklogPlan(ctx, gen.UpdateBacklogPlanParams{
		ID: p.ID, Status: string(p.Status), PlannerSessionID: string(p.PlannerSessionID),
		BaseCommit: p.BaseCommit, BaseBranch: p.BaseBranch, QuestionsJson: questions, AssumptionsJson: assumptions,
		AnswersJson: answers, PlanMarkdown: p.PlanMarkdown, DecisionsMarkdown: p.DecisionsMD, PRSplitJson: split,
		ReviewFocusJson: focus, ApprovedBy: p.ApprovedBy, ApprovedAt: approved, OwnerMessageID: p.OwnerMessageID,
		BuildAnyway: build, ItemChangedAt: changed, UpdatedAt: p.UpdatedAt, Failure: p.Failure,
	})
}

// orEmpty keeps a nil slice from encoding as JSON null.
func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
