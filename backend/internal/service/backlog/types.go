package backlog

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

type spawnerKey struct{}

// WithSpawner carries the session that asked for a spawn into
// PrepareSpawnPlan, so a plan reserved in place notifies its spawner.
func WithSpawner(ctx context.Context, a Actor) context.Context {
	return context.WithValue(ctx, spawnerKey{}, a)
}

func spawnerFrom(ctx context.Context) Actor {
	if a, ok := ctx.Value(spawnerKey{}).(Actor); ok {
		return a
	}
	return Actor{Kind: domain.BacklogOriginHuman}
}

// Actor is who performs a request, taken from the caller's credentials, never
// from the request body.
type Actor struct {
	Kind      domain.BacklogOriginKind
	SessionID string
}

// CreateInput describes a new item. Open asks to skip the inbox; it is honoured
// for humans (always open) and orchestrators, never for workers.
type CreateInput struct {
	Title      string
	Note       string
	DoneWhen   string
	Scope      domain.BacklogScope
	Hold       string
	ParentRef  string
	AgentNotes string
	Open       bool
	Excerpt    string
}

// UpdateInput is a partial edit: nil leaves a field unchanged. ParentRef "" with
// ClearParent detaches the item.
type UpdateInput struct {
	Title       *string
	Note        *string
	DoneWhen    *string
	Scope       *domain.BacklogScope
	Hold        *string
	AgentNotes  *string
	ParentRef   *string
	ClearParent bool
}

// ListFilter narrows List.
type ListFilter struct {
	State     domain.BacklogState
	ParentRef string
	Held      string
	Query     string
	Limit     int
}

// MoveInput places an item before or after another.
type MoveInput struct {
	Ref       string
	BeforeRef string
	AfterRef  string
}

// LinkView is a link with its item target rendered as a ref.
type LinkView struct {
	domain.BacklogLink
	// Display is the target item's ref ("B3") for item links.
	Display string
}

// View is an item with presentation extras.
type View struct {
	domain.BacklogItem
	ParentRef  string
	LinkViews  []LinkView
	ChildCount int
	// ChildClosedCount is how many of the children are done or dropped.
	ChildClosedCount int
	Badges           Badges
	// OpenBlockers are the refs of blocked_by targets that are not done or dropped; spawn refuses the item unless forced.
	OpenBlockers []string
	// Plan is the item's plan summary; nil while the planner is off or unplanned.
	Plan *PlanSummary
	// AutoClose says why an open item with a merged linked PR has not closed.
	AutoClose string
}
