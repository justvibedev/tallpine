package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// syncBacklogParent keeps a parent consistent with its children after one of
// them changed state. Closure is automatic only for parents this rule closed
// (closed_reason "children closed"): such a parent is reopened as soon as any
// child is live again, and a parent closed by hand is never reopened.
// Nesting is one level, so the rule never recurses.
func syncBacklogParent(ctx context.Context, q *gen.Queries, parentID string, now time.Time) error {
	if parentID == "" {
		return nil
	}
	parent, err := q.SelectBacklogItemByID(ctx, parentID)
	if err != nil {
		return fmt.Errorf("select backlog parent %s: %w", parentID, err)
	}
	children, err := q.SelectBacklogChildren(ctx, nullString(parentID))
	if err != nil {
		return err
	}
	allClosed := len(children) > 0
	for _, c := range children {
		if !domain.BacklogState(c.State).Closed() {
			allClosed = false
			break
		}
	}
	switch {
	case allClosed && (parent.State == string(domain.BacklogOpen) || parent.State == string(domain.BacklogInbox)):
		_, err = q.UpdateBacklogItemState(ctx, gen.UpdateBacklogItemStateParams{
			State: string(domain.BacklogDone), ClosedAt: sql.NullTime{Time: now, Valid: true},
			ClosedReason: nullString(domain.BacklogReasonChildrenClosed), UpdatedAt: now, ID: parentID,
		})
	case !allClosed && parent.State == string(domain.BacklogDone) && parent.ClosedReason.String == domain.BacklogReasonChildrenClosed:
		_, err = q.UpdateBacklogItemState(ctx, gen.UpdateBacklogItemStateParams{
			State: string(domain.BacklogOpen), UpdatedAt: now, ID: parentID,
		})
	}
	return err
}

// reopenAutoClosedParent reopens a parent that was closed because its children
// closed, when a live child is added or moved under it.
func reopenAutoClosedParent(ctx context.Context, q *gen.Queries, parentID string, now time.Time) error {
	return syncBacklogParent(ctx, q, parentID, now)
}
