package apitypes

import (
	"time"
)

// BacklogBadgesResponse are the badges derived from linked sessions and PRs at read time.
type BacklogBadgesResponse struct {
	Active   bool `json:"active" description:"A linked session is still running."`
	InReview bool `json:"inReview" description:"A linked PR is open or draft."`
	Landed   bool `json:"landed" description:"Every linked PR merged but the item has not closed yet."`
}

// BacklogDeleteResponse lists the refs a delete or purge removed (children
// before their parent) and the parents a purge left because a child is open.
type BacklogDeleteResponse struct {
	Deleted []string `json:"deleted"`
	Skipped []string `json:"skipped"`
}

// BacklogItemPlan is the plan state shown on a backlog item.
type BacklogItemPlan struct {
	ID          string `json:"id"`
	Status      string `json:"status" enum:"drafting,awaiting_answers,writing,awaiting_approval,approved,superseded,failed"`
	Stale       bool   `json:"stale" description:"Approved plan whose named files changed on the default branch since its base commit."`
	ItemChanged bool   `json:"itemChanged" description:"The item title, note or done-when was edited after planning began."`
}

// BacklogItemResponse is one backlog item.
type BacklogItemResponse struct {
	ID               string                `json:"id"`
	Ref              string                `json:"ref" description:"Short per-project reference, e.g. B12."`
	ProjectID        string                `json:"projectId"`
	Title            string                `json:"title"`
	Note             string                `json:"note" description:"Markdown body."`
	DoneWhen         string                `json:"doneWhen" description:"Acceptance criteria; empty when unset."`
	State            string                `json:"state" description:"inbox, open, done or dropped."`
	Hold             string                `json:"hold" description:"Free-text hold; empty means actionable."`
	Scope            string                `json:"scope,omitempty" description:"Publishing-scope override: local, pr or pr-merge."`
	ParentID         string                `json:"parentId,omitempty"`
	ParentRef        string                `json:"parentRef,omitempty"`
	ChildCount       int                   `json:"childCount" description:"Children of this item, in any state."`
	ChildClosedCount int                   `json:"childClosedCount" description:"How many of the children are done or dropped."`
	Position         int64                 `json:"position" description:"Drag order; lower is higher."`
	Origin           BacklogOriginResponse `json:"origin"`
	AgentNotes       string                `json:"agentNotes" description:"Markdown notes kept by the orchestrator."`
	Fields           map[string]string     `json:"fields" description:"Custom key to value fields."`
	ClosedReason     string                `json:"closedReason,omitempty"`
	CreatedAt        time.Time             `json:"createdAt"`
	UpdatedAt        time.Time             `json:"updatedAt"`
	ClosedAt         *time.Time            `json:"closedAt,omitempty"`
	Links            []BacklogLinkResponse `json:"links" description:"Present on single-item reads."`
	LinkCounts       map[string]int        `json:"linkCounts" description:"Per-kind link counts on list reads."`
	AutoClose        string                `json:"autoClose,omitempty" description:"Set on an open item with a merged linked PR: why it has not auto-closed (scope local, another PR still open, untracked PR link, ...)."`
	Badges           BacklogBadgesResponse `json:"badges"`
	Plan             *BacklogItemPlan      `json:"plan,omitempty" description:"The item's plan summary on single-item reads; absent while the planner is off or the item is unplanned."`
}

// BacklogLinkRequest is the body of POST and DELETE .../backlog/{ref}/links.
type BacklogLinkRequest struct {
	Kind string `json:"kind" description:"session, pr, issue, artifact, memory, item or blocked_by."`
	Ref  string `json:"ref" description:"Target reference; for item and blocked_by an item ref (B3) or id."`
}

// BacklogLinkResponse is one typed link from an item.
type BacklogLinkResponse struct {
	Kind      string    `json:"kind" description:"session, pr, issue, artifact, memory, item or blocked_by."`
	Ref       string    `json:"ref" description:"Target reference; an item id for item and blocked_by links."`
	Display   string    `json:"display" description:"Human label: the target's ref (B3) for item links, else ref."`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
}

// BacklogOriginResponse says who filed an item.
type BacklogOriginResponse struct {
	Kind      string `json:"kind" description:"human, orchestrator, worker or reviewer."`
	SessionID string `json:"sessionId,omitempty" description:"Filing session, when an agent filed it."`
	Excerpt   string `json:"excerpt,omitempty" description:"Source text the item was derived from."`
}

// BacklogPurgeRequest is the body of POST /projects/{projectId}/backlog/purge.
type BacklogPurgeRequest struct {
	States        []string `json:"states" description:"Closed states to purge: done and/or dropped."`
	OlderThanDays *int     `json:"olderThanDays,omitempty" description:"Only items closed more than this many days ago."`
	DryRun        bool     `json:"dryRun,omitempty" description:"List what would be deleted without deleting."`
}

// BacklogReorderRequest is the body of POST /projects/{projectId}/backlog/reorder.
type BacklogReorderRequest struct {
	Ref    string `json:"ref"`
	Before string `json:"before,omitempty" description:"Place directly before this item."`
	After  string `json:"after,omitempty" description:"Place directly after this item."`
}

// BacklogStateRequest is the body of POST .../backlog/{ref}/state.
type BacklogStateRequest struct {
	State  string `json:"state" description:"inbox, open, done or dropped."`
	Reason string `json:"reason,omitempty" description:"Why it closed; kept for done and dropped."`
}

// CreateBacklogRequest is the body of POST /projects/{projectId}/backlog.
type CreateBacklogRequest struct {
	Title      string `json:"title"`
	Note       string `json:"note,omitempty"`
	DoneWhen   string `json:"doneWhen,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Hold       string `json:"hold,omitempty"`
	Parent     string `json:"parent,omitempty" description:"Ref of the parent item (one level only)."`
	AgentNotes string `json:"agentNotes,omitempty"`
	Open       bool   `json:"open,omitempty" description:"Skip the inbox. Ignored for workers, who always file to the inbox."`
	Excerpt    string `json:"excerpt,omitempty"`
}
