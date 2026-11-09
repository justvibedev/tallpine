package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Badges are derived from linked sessions and PRs at read time and never stored.
type Badges struct {
	// Active: a linked session is still running.
	Active bool
	// InReview: a linked PR is open or draft.
	InReview bool
	// Landed: every linked PR merged but the item is not closed yet (stays until it is closed by hand when a merge left it open without a claim).
	Landed bool
}

// DeriveBadges computes the badges for a non-closed item from its facts. A
// closed item has none.
func DeriveBadges(state domain.BacklogState, f store.BacklogBadgeFacts) Badges {
	if state.Closed() {
		return Badges{}
	}
	total := f.OpenPRs + f.MergedPRs + f.ClosedPRs
	return Badges{
		Active:   f.ActiveSessions > 0,
		InReview: f.OpenPRs > 0,
		Landed:   total > 0 && f.MergedPRs == total && f.UntrackedPRs == 0,
	}
}

// attachBadges fills Badges on views from one project-wide query.
func (s *Service) attachBadges(ctx context.Context, projectID domain.ProjectID, itemID string, views []View) error {
	facts, err := s.store.BacklogBadgeFactsFor(ctx, projectID, itemID)
	if err != nil {
		return err
	}
	for i := range views {
		bf := facts[views[i].ID]
		views[i].Badges = DeriveBadges(views[i].State, bf)
		if views[i].State.Closed() || bf.MergedPRs == 0 {
			continue
		}
		// A merged PR sits behind this still-open item: say why it did not close.
		pf, err := s.store.BacklogFactsFor(ctx, views[i].ID)
		if err != nil {
			return err
		}
		views[i].AutoClose = DecideAutoClose(views[i].BacklogItem, pf).Explain()
	}
	return nil
}
