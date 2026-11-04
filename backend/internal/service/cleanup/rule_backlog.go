package cleanup

import (
	"context"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// BacklogInventory lists and expires backlog inbox items.
type BacklogInventory interface {
	ListExpiredBacklogInbox(ctx context.Context, cutoff time.Time) ([]domain.BacklogItem, error)
	// ExpireBacklogInboxItem re-checks, atomically, that the item is still an
	// untouched inbox item before dropping it.
	ExpireBacklogInboxItem(ctx context.Context, id string, cutoff time.Time, reason string, now time.Time) (domain.BacklogItem, bool, error)
}

// BacklogRule drops agent-filed inbox items nobody touched for the policy's
// InboxExpiryDays. "Touched" is updated_at: edits, new links and a re-filed
// duplicate all count. Human-filed items never expire. The item is marked
// dropped with closed_reason "expired" and stays in the archive.
type BacklogRule struct{ backlog BacklogInventory }

// NewBacklogRule builds the rule.
func NewBacklogRule(backlog BacklogInventory) *BacklogRule { return &BacklogRule{backlog: backlog} }

// Category implements Rule.
func (*BacklogRule) Category() Category { return CategoryBacklog }

// Light implements Rule.
func (*BacklogRule) Light() bool { return true }

// Enabled implements Rule.
func (*BacklogRule) Enabled(p Policy) bool { return p.Backlog.Enabled }

// Run implements Rule.
func (r *BacklogRule) Run(ctx context.Context, run *Run) error {
	cutoff := run.Now.Add(-time.Duration(run.Policy.Backlog.InboxExpiryDays) * 24 * time.Hour)
	items, err := r.backlog.ListExpiredBacklogInbox(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Origin.Kind == domain.BacklogOriginHuman || it.State != domain.BacklogInbox {
			continue
		}
		item := Item{ProjectID: it.ProjectID, What: fmt.Sprintf("backlog item %s %q", it.Ref(), it.Title), Status: StatusWouldFree}
		if !run.DryRun {
			if _, changed, err := r.backlog.ExpireBacklogInboxItem(ctx, it.ID, cutoff, domain.BacklogReasonExpired, run.Now.UTC()); err != nil {
				item.Status, item.Reason = StatusFailed, err.Error()
			} else if changed {
				item.Status = StatusFreed
			} else {
				item.Status, item.Reason = StatusKept, "it was accepted, edited or closed meanwhile"
			}
			_ = run.Pace(ctx)
		}
		run.Add(item)
	}
	return ctx.Err()
}
