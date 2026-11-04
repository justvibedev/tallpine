package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// checkBacklogParent enforces the one-level hierarchy for item (id may be empty
// for a not-yet-inserted item) taking parentID as its parent: the parent must
// exist in the same project, must not itself have a parent, and the item must
// not already have children.
func checkBacklogParent(ctx context.Context, q *gen.Queries, projectID domain.ProjectID, itemID, parentID string) error {
	if parentID == "" {
		return nil
	}
	if parentID == itemID {
		return fmt.Errorf("%w: an item cannot be its own parent", domain.ErrBacklogHierarchy)
	}
	parent, err := q.SelectBacklogItemByID(ctx, parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: parent %s does not exist", domain.ErrBacklogHierarchy, parentID)
	}
	if err != nil {
		return err
	}
	if domain.ProjectID(parent.ProjectID) != projectID {
		return fmt.Errorf("%w: parent belongs to another project", domain.ErrBacklogCrossProject)
	}
	if parent.ParentID.Valid {
		return fmt.Errorf("%w: only one level of nesting is allowed (the parent is itself a child)", domain.ErrBacklogHierarchy)
	}
	if itemID != "" {
		n, err := q.CountBacklogChildren(ctx, nullString(itemID))
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: the item has children and cannot become a child", domain.ErrBacklogHierarchy)
		}
	}
	return nil
}
