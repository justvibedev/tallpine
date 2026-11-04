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

// ListBacklogRunItems returns a run's planned items in lane/position order.
func (s *Store) ListBacklogRunItems(ctx context.Context, runID string) ([]domain.BacklogRunItem, error) {
	return listBacklogRunItems(ctx, s.qr, runID)
}

func listBacklogRunItems(ctx context.Context, q *gen.Queries, runID string) ([]domain.BacklogRunItem, error) {
	rows, err := q.SelectBacklogRunItems(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("list backlog run items: %w", err)
	}
	out := make([]domain.BacklogRunItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.BacklogRunItem{
			RunID: r.RunID, ItemID: r.ItemID, Lane: int(r.Lane), Position: int(r.Position), Model: r.Model,
			Status: domain.BacklogRunItemStatus(r.Status), Reason: r.Reason, Question: r.Question,
			Attempts: int(r.Attempts), UpdatedAt: r.UpdatedAt, ItemSeq: r.ItemSeq, ItemTitle: r.ItemTitle,
			ItemState: domain.BacklogState(r.ItemState), ItemHold: r.ItemHold,
		})
	}
	return out, nil
}

// BacklogRunPlanItem is one entry of a plan being saved.
type BacklogRunPlanItem struct {
	ItemID   string
	Lane     int
	Position int
	Model    string
}

// SaveBacklogRunPlan replaces the run's planned items. Items already in the
// run keep their status, reason and attempts; new ones start queued; items
// left out of the plan are removed. mutate edits the run row in the same
// transaction (the service flips planning to running there).
func (s *Store) SaveBacklogRunPlan(ctx context.Context, runID string, plan []BacklogRunPlanItem, now time.Time, mutate func(*domain.BacklogRun) error) (domain.BacklogRun, error) {
	return s.SaveBacklogRunPlanFrom(ctx, runID, now, func(*domain.BacklogRun, []domain.BacklogRunItem) ([]BacklogRunPlanItem, error) {
		return plan, nil
	}, mutate)
}

// SaveBacklogRunPlanFrom is SaveBacklogRunPlan with the plan built inside the
// transaction from the run and its items as they are at that moment, so a
// concurrent change is never overwritten. An error from build aborts the save.
func (s *Store) SaveBacklogRunPlanFrom(ctx context.Context, runID string, now time.Time, build func(*domain.BacklogRun, []domain.BacklogRunItem) ([]BacklogRunPlanItem, error), mutate func(*domain.BacklogRun) error) (domain.BacklogRun, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	run, err := loadBacklogRun(ctx, q, runID)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	existing, err := listBacklogRunItems(ctx, q, runID)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	plan, err := build(&run, existing)
	if err != nil {
		return domain.BacklogRun{}, err
	}
	byItem := map[string]domain.BacklogRunItem{}
	for _, it := range existing {
		byItem[it.ItemID] = it
	}
	for _, p := range plan {
		cur, had := byItem[p.ItemID]
		delete(byItem, p.ItemID)
		if had {
			err = q.UpdateBacklogRunItem(ctx, gen.UpdateBacklogRunItemParams{
				Lane: int64(p.Lane), Position: int64(p.Position), Model: p.Model, Status: string(cur.Status),
				Reason: cur.Reason, Question: cur.Question, Attempts: int64(cur.Attempts), UpdatedAt: now,
				RunID: runID, ItemID: p.ItemID,
			})
		} else {
			err = q.InsertBacklogRunItem(ctx, gen.InsertBacklogRunItemParams{
				RunID: runID, ItemID: p.ItemID, Lane: int64(p.Lane), Position: int64(p.Position), Model: p.Model,
				Status: string(domain.BacklogRunItemQueued), UpdatedAt: now,
			})
		}
		if err != nil {
			return domain.BacklogRun{}, fmt.Errorf("save backlog run plan: %w", err)
		}
	}
	for itemID, gone := range byItem {
		// An item with a worker, a result or a failure on record must stay: dropping
		// it would hide a running worker from the cap and reset its attempts.
		if gone.Status != domain.BacklogRunItemQueued || gone.Attempts > 0 {
			return domain.BacklogRun{}, fmt.Errorf("%w: %s is %s; it must stay in the plan", domain.ErrBacklogRunInvalid, gone.ItemRef(), gone.Status)
		}
		if err := q.DeleteBacklogRunItem(ctx, gen.DeleteBacklogRunItemParams{RunID: runID, ItemID: itemID}); err != nil {
			return domain.BacklogRun{}, fmt.Errorf("save backlog run plan: %w", err)
		}
	}
	if mutate != nil {
		if err := mutate(&run); err != nil {
			return domain.BacklogRun{}, err
		}
		run.UpdatedAt = now
		if err := writeBacklogRun(ctx, q, run); err != nil {
			return domain.BacklogRun{}, err
		}
	}
	return run, tx.Commit()
}

// UpdateBacklogRunState lets the service change a run and its items in one
// transaction. mutate sees the run, its items and how many items hold a worker
// slot in the project's open runs (this run's included); every field it changes
// is written back, and nothing is written when it returns an error.
func (s *Store) UpdateBacklogRunState(ctx context.Context, runID string, now time.Time, mutate func(run *domain.BacklogRun, items []domain.BacklogRunItem, projectActive int) error) (domain.BacklogRun, []domain.BacklogRunItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogRun{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	run, err := loadBacklogRun(ctx, q, runID)
	if err != nil {
		return domain.BacklogRun{}, nil, err
	}
	items, err := listBacklogRunItems(ctx, q, runID)
	if err != nil {
		return domain.BacklogRun{}, nil, err
	}
	active, err := q.CountBacklogRunActiveInProject(ctx, string(run.ProjectID))
	if err != nil {
		return domain.BacklogRun{}, nil, fmt.Errorf("count active run items: %w", err)
	}
	beforeRun := run
	before := append([]domain.BacklogRunItem(nil), items...)
	if err := mutate(&run, items, int(active)); err != nil {
		return domain.BacklogRun{}, nil, err
	}
	for i, it := range items {
		if it == before[i] {
			continue
		}
		it.UpdatedAt = now
		items[i] = it
		err := q.UpdateBacklogRunItem(ctx, gen.UpdateBacklogRunItemParams{
			Lane: int64(it.Lane), Position: int64(it.Position), Model: it.Model, Status: string(it.Status),
			Reason: it.Reason, Question: it.Question, Attempts: int64(it.Attempts), UpdatedAt: now,
			RunID: it.RunID, ItemID: it.ItemID,
		})
		if err != nil {
			return domain.BacklogRun{}, nil, fmt.Errorf("update backlog run item: %w", err)
		}
	}
	if !sameBacklogRun(beforeRun, run) {
		run.UpdatedAt = now
		if err := writeBacklogRun(ctx, q, run); err != nil {
			return domain.BacklogRun{}, nil, err
		}
	}
	return run, items, tx.Commit()
}

func loadBacklogRun(ctx context.Context, q *gen.Queries, runID string) (domain.BacklogRun, error) {
	row, err := q.SelectBacklogRunByID(ctx, runID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogRun{}, domain.ErrBacklogRunNotFound
	}
	if err != nil {
		return domain.BacklogRun{}, fmt.Errorf("select backlog run: %w", err)
	}
	return backlogRunFromGen(row)
}

func writeBacklogRun(ctx context.Context, q *gen.Queries, r domain.BacklogRun) error {
	err := q.UpdateBacklogRun(ctx, gen.UpdateBacklogRunParams{
		Status: string(r.Status), PauseReason: r.PauseReason, ResumeAt: timePtrToNullTime(r.ResumeAt),
		Summary: r.Summary, UpdatedAt: r.UpdatedAt, StartedAt: timePtrToNullTime(r.StartedAt),
		EndedAt: timePtrToNullTime(r.EndedAt), ID: r.ID,
	})
	if err != nil {
		return fmt.Errorf("update backlog run: %w", err)
	}
	return nil
}

// sameBacklogRun compares the mutable fields (the selection never changes).
func sameBacklogRun(a, b domain.BacklogRun) bool {
	return a.Status == b.Status && a.PauseReason == b.PauseReason && a.Summary == b.Summary &&
		ptrTimeEqual(a.ResumeAt, b.ResumeAt) && ptrTimeEqual(a.StartedAt, b.StartedAt) && ptrTimeEqual(a.EndedAt, b.EndedAt)
}

func ptrTimeEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
