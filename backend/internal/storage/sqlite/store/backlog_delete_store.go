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

// BacklogPurgeInput selects closed items to hard-delete.
type BacklogPurgeInput struct {
	ProjectID domain.ProjectID
	Done      bool
	Dropped   bool
	// ClosedBefore, when set, keeps items closed at or after it.
	ClosedBefore *time.Time
	// DryRun reports what would be deleted and changes nothing.
	DryRun bool
}

// BacklogPurgeResult lists what a purge deleted (children before their
// parent) and the matching parents it left because a child is still open.
type BacklogPurgeResult struct {
	Deleted []domain.BacklogItem
	Skipped []domain.BacklogItem
}

// DeleteBacklogItem hard-deletes a closed item and its closed children and
// returns the deleted items, children first. An item that is not closed, or a
// parent with any open child, is refused with ErrBacklogNotClosed. Links and
// run rows cascade; the project's ref counter is untouched, so refs are never
// reused.
func (s *Store) DeleteBacklogItem(ctx context.Context, id string) ([]domain.BacklogItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	row, err := q.SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrBacklogNotFound
	}
	if err != nil {
		return nil, err
	}
	tree, err := closedBacklogTree(ctx, q, row)
	if err != nil {
		return nil, err
	}
	if err := deleteBacklogTree(ctx, q, tree); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return tree, nil
}

// PurgeBacklog hard-deletes every closed item matching the input (and the
// closed children of each matched parent). A matched parent with an open child
// is skipped, not an error.
func (s *Store) PurgeBacklog(ctx context.Context, in BacklogPurgeInput) (BacklogPurgeResult, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return BacklogPurgeResult{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	rows, err := q.SelectClosedBacklogForPurge(ctx, gen.SelectClosedBacklogForPurgeParams{
		ProjectID: string(in.ProjectID), StateDone: boolInt(in.Done), StateDropped: boolInt(in.Dropped),
	})
	if err != nil {
		return BacklogPurgeResult{}, err
	}
	var res BacklogPurgeResult
	gone := map[string]bool{}
	for _, row := range rows {
		if gone[row.ID] {
			continue
		}
		if in.ClosedBefore != nil && (!row.ClosedAt.Valid || !row.ClosedAt.Time.Before(*in.ClosedBefore)) {
			continue
		}
		tree, err := closedBacklogTree(ctx, q, row)
		if errors.Is(err, domain.ErrBacklogNotClosed) {
			item, convErr := backlogItemFromGen(row)
			if convErr != nil {
				return BacklogPurgeResult{}, convErr
			}
			res.Skipped = append(res.Skipped, item)
			continue
		}
		if err != nil {
			return BacklogPurgeResult{}, err
		}
		// A dry run deletes nothing, so a child already reported (it can have a
		// lower seq than its parent after a re-parent) is still in the tree.
		fresh := tree[:0:0]
		for _, it := range tree {
			if !gone[it.ID] {
				fresh = append(fresh, it)
			}
		}
		tree = fresh
		if !in.DryRun {
			if err := deleteBacklogTree(ctx, q, tree); err != nil {
				return BacklogPurgeResult{}, err
			}
		}
		for _, it := range tree {
			gone[it.ID] = true
		}
		res.Deleted = append(res.Deleted, tree...)
	}
	if in.DryRun {
		return res, nil
	}
	if err := tx.Commit(); err != nil {
		return BacklogPurgeResult{}, err
	}
	return res, nil
}

// closedBacklogTree returns the item's children (all must be closed) followed
// by the item itself, which must be closed.
func closedBacklogTree(ctx context.Context, q *gen.Queries, row gen.BacklogItem) ([]domain.BacklogItem, error) {
	item, err := backlogItemFromGen(row)
	if err != nil {
		return nil, err
	}
	if !item.State.Closed() {
		return nil, fmt.Errorf("%w: B%d is %s; only done or dropped items can be deleted", domain.ErrBacklogNotClosed, item.Seq, item.State)
	}
	kids, err := q.SelectBacklogChildren(ctx, nullString(item.ID))
	if err != nil {
		return nil, err
	}
	tree := make([]domain.BacklogItem, 0, len(kids)+1)
	for _, k := range kids {
		child, err := backlogItemFromGen(k)
		if err != nil {
			return nil, err
		}
		if !child.State.Closed() {
			return nil, fmt.Errorf("%w: B%d has child B%d still %s; close or delete it first", domain.ErrBacklogNotClosed, item.Seq, child.Seq, child.State)
		}
		tree = append(tree, child)
	}
	return append(tree, item), nil
}

// deleteBacklogTree deletes the items in order and writes one backlog_changed
// row per item.
func deleteBacklogTree(ctx context.Context, q *gen.Queries, tree []domain.BacklogItem) error {
	for _, it := range tree {
		n, err := q.DeleteBacklogItem(ctx, it.ID)
		if err != nil {
			return fmt.Errorf("delete backlog item %s: %w", it.ID, err)
		}
		if n == 0 {
			continue
		}
		if err := q.InsertBacklogChangeLog(ctx, gen.InsertBacklogChangeLogParams{
			ProjectID: &it.ProjectID,
			ItemID:    it.ID, Ref: fmt.Sprintf("B%d", it.Seq), State: string(it.State),
		}); err != nil {
			return fmt.Errorf("record backlog delete: %w", err)
		}
	}
	return nil
}
