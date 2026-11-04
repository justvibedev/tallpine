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

// backlogPositionStep is the gap between consecutive item positions.
const backlogPositionStep = 1024

// insertBacklogItem allocates the item's ref and position, checks the
// hierarchy and inserts it. The caller owns the write lock and transaction.
func insertBacklogItem(ctx context.Context, q *gen.Queries, in domain.BacklogItem) (domain.BacklogItem, error) {
	if err := checkBacklogParent(ctx, q, in.ProjectID, "", in.ParentID); err != nil {
		return domain.BacklogItem{}, err
	}
	seq, err := q.AllocateBacklogSeq(ctx, string(in.ProjectID))
	if err != nil {
		if isSQLiteForeignKey(err) {
			return domain.BacklogItem{}, domain.ErrProjectUnknown
		}
		return domain.BacklogItem{}, fmt.Errorf("allocate backlog ref: %w", err)
	}
	maxPos, err := q.SelectBacklogMaxPosition(ctx, string(in.ProjectID))
	if err != nil {
		return domain.BacklogItem{}, err
	}
	in.Seq = seq
	in.Position = maxPos + backlogPositionStep
	fields, err := fieldsJSON(in.Fields)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	var closedAt sql.NullTime
	if in.ClosedAt != nil {
		closedAt = sql.NullTime{Time: *in.ClosedAt, Valid: true}
	}
	err = q.InsertBacklogItem(ctx, gen.InsertBacklogItemParams{
		ID: in.ID, ProjectID: string(in.ProjectID), Seq: in.Seq, Title: in.Title, Note: in.Note,
		DoneWhen: in.DoneWhen, State: string(in.State), Hold: in.Hold, Scope: nullString(string(in.Scope)),
		ParentID: nullString(in.ParentID), Position: in.Position, OriginKind: string(in.Origin.Kind),
		OriginSessionID: in.Origin.SessionID, OriginExcerpt: in.Origin.Excerpt, AgentNotes: in.AgentNotes,
		Fields: fields, ClosedReason: nullString(in.ClosedReason),
		CreatedAt: in.CreatedAt, UpdatedAt: in.UpdatedAt, ClosedAt: closedAt,
	})
	if err != nil {
		return domain.BacklogItem{}, fmt.Errorf("insert backlog item: %w", err)
	}
	if in.ParentID != "" && !in.State.Closed() {
		if err := reopenAutoClosedParent(ctx, q, in.ParentID, in.UpdatedAt); err != nil {
			return domain.BacklogItem{}, err
		}
	}
	return in, nil
}

// CreateBacklogItem inserts a new item, allocating its per-project ref and
// appending it at the end of the order. The caller supplies the id, state,
// origin and timestamps.
func (s *Store) CreateBacklogItem(ctx context.Context, in domain.BacklogItem) (domain.BacklogItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := insertBacklogItem(ctx, s.qw.WithTx(tx), in)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, err
	}
	return item, nil
}

// GetBacklogItem returns one item with its links. ok is false when unknown.
func (s *Store) GetBacklogItem(ctx context.Context, id string) (domain.BacklogItem, bool, error) {
	row, err := s.qr.SelectBacklogItemByID(ctx, id)
	return s.finishBacklogGet(ctx, row, err)
}

// GetBacklogItemByRef resolves a project-scoped sequence number to an item.
func (s *Store) GetBacklogItemByRef(ctx context.Context, projectID domain.ProjectID, seq int64) (domain.BacklogItem, bool, error) {
	row, err := s.qr.SelectBacklogItemByRef(ctx, gen.SelectBacklogItemByRefParams{ProjectID: string(projectID), Seq: seq})
	return s.finishBacklogGet(ctx, row, err)
}

func (s *Store) finishBacklogGet(ctx context.Context, row gen.BacklogItem, err error) (domain.BacklogItem, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, false, nil
	}
	if err != nil {
		return domain.BacklogItem{}, false, fmt.Errorf("select backlog item: %w", err)
	}
	item, err := backlogItemFromGen(row)
	if err != nil {
		return domain.BacklogItem{}, false, err
	}
	links, err := s.qr.SelectBacklogLinks(ctx, item.ID)
	if err != nil {
		return domain.BacklogItem{}, false, fmt.Errorf("select backlog links: %w", err)
	}
	item.Links = backlogLinksFromGen(links)
	return item, true, nil
}

// BacklogFilter narrows ListBacklogItems. Zero values mean "any".
type BacklogFilter struct {
	State    domain.BacklogState
	ParentID string
	// Held is "", "held" or "unheld".
	Held  string
	Query string
	Limit int
}

// ListBacklogItems returns a project's items in drag order with per-kind link
// counts.
func (s *Store) ListBacklogItems(ctx context.Context, projectID domain.ProjectID, f BacklogFilter) ([]domain.BacklogItem, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = domain.DefaultBacklogListLimit
	}
	if limit > domain.MaxBacklogListLimit {
		limit = domain.MaxBacklogListLimit
	}
	rows, err := s.qr.SelectBacklogItemsByProject(ctx, gen.SelectBacklogItemsByProjectParams{
		ProjectID: string(projectID), State: string(f.State), ParentID: f.ParentID,
		Held: f.Held, Query: f.Query, RowLimit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list backlog items: %w", err)
	}
	items, err := backlogItemsFromGen(rows)
	if err != nil {
		return nil, err
	}
	counts, err := s.qr.SelectBacklogLinkCounts(ctx, string(projectID))
	if err != nil {
		return nil, fmt.Errorf("count backlog links: %w", err)
	}
	byItem := map[string]map[domain.BacklogLinkKind]int{}
	for _, c := range counts {
		if byItem[c.ItemID] == nil {
			byItem[c.ItemID] = map[domain.BacklogLinkKind]int{}
		}
		byItem[c.ItemID][domain.BacklogLinkKind(c.Kind)] = int(c.N)
	}
	for i := range items {
		items[i].LinkCounts = byItem[items[i].ID]
	}
	return items, nil
}

// UpdateBacklogItem reads the item, applies mutate and writes the result in one
// transaction, so concurrent edits cannot lose each other. Only the editable
// fields (title, note, done-when, hold, scope, parent, agent notes, fields) are
// persisted; state, position and links have their own methods. A parent change
// is checked against the one-level hierarchy.
func (s *Store) UpdateBacklogItem(ctx context.Context, id string, now time.Time, mutate func(*domain.BacklogItem) error) (domain.BacklogItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.qw.WithTx(tx)
	row, err := q.SelectBacklogItemByID(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.BacklogItem{}, domain.ErrBacklogNotFound
	}
	if err != nil {
		return domain.BacklogItem{}, err
	}
	item, err := backlogItemFromGen(row)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	prevParent := item.ParentID
	if err := mutate(&item); err != nil {
		return domain.BacklogItem{}, err
	}
	if item.ParentID != prevParent {
		if err := checkBacklogParent(ctx, q, item.ProjectID, item.ID, item.ParentID); err != nil {
			return domain.BacklogItem{}, err
		}
	}
	fields, err := fieldsJSON(item.Fields)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	item.UpdatedAt = now
	if _, err := q.UpdateBacklogItem(ctx, gen.UpdateBacklogItemParams{
		Title: item.Title, Note: item.Note, DoneWhen: item.DoneWhen, Hold: item.Hold,
		Scope: nullString(string(item.Scope)), ParentID: nullString(item.ParentID),
		AgentNotes: item.AgentNotes, Fields: fields, UpdatedAt: now, ID: id,
	}); err != nil {
		return domain.BacklogItem{}, fmt.Errorf("update backlog item %s: %w", id, err)
	}
	// Moving a live child under a parent that was closed because its children
	// closed reopens it. Re-parenting away the last live child does not re-run
	// closure for the old parent.
	if item.ParentID != prevParent && item.ParentID != "" && !item.State.Closed() {
		if err := reopenAutoClosedParent(ctx, q, item.ParentID, now); err != nil {
			return domain.BacklogItem{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, err
	}
	return item, nil
}

// SetBacklogItemState moves an item between states. Closing (done/dropped)
// stamps closed_at and the reason; reopening clears both. A parent follows its
// children (see syncBacklogParent).
func (s *Store) SetBacklogItemState(ctx context.Context, id string, state domain.BacklogState, reason string, now time.Time) (domain.BacklogItem, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.writeDB.BeginTx(ctx, nil)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	defer func() { _ = tx.Rollback() }()
	item, err := s.setStateTx(ctx, tx, id, state, reason, now)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return domain.BacklogItem{}, err
	}
	return item, nil
}

// setStateTx is SetBacklogItemState inside a caller-owned transaction.
func (s *Store) setStateTx(ctx context.Context, tx *sql.Tx, id string, state domain.BacklogState, reason string, now time.Time) (domain.BacklogItem, error) {
	q := s.qw.WithTx(tx)
	var closedAt sql.NullTime
	// Reopening a closed item: the merges it already has must not close it again.
	reopening := false
	if !state.Closed() {
		prev, perr := q.SelectBacklogItemByID(ctx, id)
		if perr != nil && !errors.Is(perr, sql.ErrNoRows) {
			return domain.BacklogItem{}, perr
		}
		reopening = perr == nil && domain.BacklogState(prev.State).Closed()
	}
	if state.Closed() {
		closedAt = sql.NullTime{Time: now, Valid: true}
	} else {
		reason = ""
	}
	n, err := q.UpdateBacklogItemState(ctx, gen.UpdateBacklogItemStateParams{
		State: string(state), ClosedAt: closedAt, ClosedReason: nullString(reason), UpdatedAt: now, ID: id,
	})
	if err != nil {
		return domain.BacklogItem{}, fmt.Errorf("set backlog item %s state: %w", id, err)
	}
	if n == 0 {
		return domain.BacklogItem{}, domain.ErrBacklogNotFound
	}
	if reopening {
		if err := q.AckBacklogMergedPRsOnReopen(ctx, id); err != nil {
			return domain.BacklogItem{}, fmt.Errorf("ack merged PRs of backlog item %s: %w", id, err)
		}
	}
	row, err := q.SelectBacklogItemByID(ctx, id)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	item, err := backlogItemFromGen(row)
	if err != nil {
		return domain.BacklogItem{}, err
	}
	if err := syncBacklogParent(ctx, q, item.ParentID, now); err != nil {
		return domain.BacklogItem{}, err
	}
	return item, nil
}
