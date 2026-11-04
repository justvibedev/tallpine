package backlog

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// PurgeInput selects closed items to hard-delete.
type PurgeInput struct {
	States        []string
	OlderThanDays *int
	DryRun        bool
}

// DeleteResult lists refs ("B12") a delete or purge removed (children before
// their parent) and the parents a purge skipped because a child is still open.
type DeleteResult struct {
	Deleted []string
	Skipped []string
}

// Delete hard-deletes a closed item and its closed children.
func (s *Service) Delete(ctx context.Context, projectID domain.ProjectID, idOrRef string) (DeleteResult, error) {
	item, err := s.resolve(ctx, projectID, idOrRef)
	if err != nil {
		return DeleteResult{}, err
	}
	deleted, err := s.store.DeleteBacklogItem(ctx, item.ID)
	if err != nil {
		return DeleteResult{}, mapError(err)
	}
	return DeleteResult{Deleted: refsOf(deleted), Skipped: []string{}}, nil
}

// Purge hard-deletes the project's closed items in the given states.
func (s *Service) Purge(ctx context.Context, projectID domain.ProjectID, in PurgeInput) (DeleteResult, error) {
	var sp store.BacklogPurgeInput
	sp.ProjectID, sp.DryRun = projectID, in.DryRun
	for _, st := range in.States {
		switch domain.BacklogState(st) {
		case domain.BacklogDone:
			sp.Done = true
		case domain.BacklogDropped:
			sp.Dropped = true
		default:
			return DeleteResult{}, apierr.Invalid("INVALID_BACKLOG_STATE", "Purge states must be done and/or dropped", nil)
		}
	}
	if !sp.Done && !sp.Dropped {
		return DeleteResult{}, apierr.Invalid("INVALID_BACKLOG_STATE", "Give at least one state to purge: done or dropped", nil)
	}
	if in.OlderThanDays != nil {
		if *in.OlderThanDays < 0 {
			return DeleteResult{}, apierr.Invalid("INVALID_BACKLOG_REQUEST", "olderThanDays cannot be negative", nil)
		}
		cutoff := s.now().UTC().Add(-time.Duration(*in.OlderThanDays) * 24 * time.Hour)
		sp.ClosedBefore = &cutoff
	}
	res, err := s.store.PurgeBacklog(ctx, sp)
	if err != nil {
		return DeleteResult{}, mapError(err)
	}
	return DeleteResult{Deleted: refsOf(res.Deleted), Skipped: refsOf(res.Skipped)}, nil
}

func refsOf(items []domain.BacklogItem) []string {
	refs := make([]string, 0, len(items))
	for _, it := range items {
		refs = append(refs, fmt.Sprintf("B%d", it.Seq))
	}
	return refs
}
