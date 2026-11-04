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

// MoveBacklogItem places id directly before (or, with after, directly after)
// targetID in the project's drag order. Positions of the whole project are
// renumbered in the same transaction; backlogs are small, so this is simpler
// than gap arithmetic and cannot run out of room.
func (s *Store) MoveBacklogItem(ctx context.Context, id, targetID string, after bool, now time.Time) error {
	if id == targetID {
		return fmt.Errorf("%w: cannot move an item relative to itself", domain.ErrBacklogInvalid)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	moved, err := q.SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrBacklogNotFound
	}
	if err != nil {
		return err
	}
	target, err := q.SelectBacklogItemByID(ctx, targetID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrBacklogNotFound
	}
	if err != nil {
		return err
	}
	if moved.ProjectID != target.ProjectID {
		return domain.ErrBacklogCrossProject
	}
	rows, err := q.SelectBacklogPositions(ctx, moved.ProjectID)
	if err != nil {
		return err
	}
	ordered := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.ID != id {
			ordered = append(ordered, r.ID)
		}
	}
	at := -1
	for i, oid := range ordered {
		if oid == targetID {
			at = i
		}
	}
	if at < 0 {
		return domain.ErrBacklogNotFound
	}
	if after {
		at++
	}
	ordered = append(ordered[:at], append([]string{id}, ordered[at:]...)...)
	current := make(map[string]int64, len(rows))
	for _, r := range rows {
		current[r.ID] = r.Position
	}
	for i, oid := range ordered {
		want := int64(i+1) * backlogPositionStep
		if current[oid] == want {
			continue
		}
		if err := q.UpdateBacklogItemPosition(ctx, gen.UpdateBacklogItemPositionParams{Position: want, UpdatedAt: now, ID: oid}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NextBacklogItem returns the topmost open, unheld, non-parent item that no
// open or inbox blocker holds back. A non-empty parentID restricts the search
// to that parent's children. ok is false when nothing qualifies.
func (s *Store) NextBacklogItem(ctx context.Context, projectID domain.ProjectID, parentID string) (domain.BacklogItem, bool, error) {
	row, err := s.qr.SelectNextBacklogItem(ctx, gen.SelectNextBacklogItemParams{ProjectID: string(projectID), ParentID: parentID})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, false, nil
	}
	if err != nil {
		return domain.BacklogItem{}, false, fmt.Errorf("select next backlog item: %w", err)
	}
	item, err := backlogItemFromGen(row)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	return item, true, nil
}

// CountBacklogChildren reports how many children an item has; a parent
// (count > 0) cannot be started directly.
func (s *Store) CountBacklogChildren(ctx context.Context, id string) (int, error) {
	n, err := s.qr.CountBacklogChildren(ctx, nullString(id))
	return int(n), err
}
