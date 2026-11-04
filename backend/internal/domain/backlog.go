package domain

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// BacklogState is the stored lifecycle state of a backlog item. Progress
// ("active", "in review") is derived from links at read time, never stored.
type BacklogState string

// Backlog item states.
const (
	BacklogInbox   BacklogState = "inbox"
	BacklogOpen    BacklogState = "open"
	BacklogDone    BacklogState = "done"
	BacklogDropped BacklogState = "dropped"
)

// Valid reports whether s is a stored state.
func (s BacklogState) Valid() bool {
	switch s {
	case BacklogInbox, BacklogOpen, BacklogDone, BacklogDropped:
		return true
	}
	return false
}

// Closed reports whether the state ends the item's life.
func (s BacklogState) Closed() bool { return s == BacklogDone || s == BacklogDropped }

// BacklogScope is a per-item publishing-scope override. Empty means "ask".
type BacklogScope string

// Backlog scopes.
const (
	BacklogScopeLocal   BacklogScope = "local"
	BacklogScopePR      BacklogScope = "pr"
	BacklogScopePRMerge BacklogScope = "pr-merge"
)

// Valid reports whether s is empty (no override) or a known scope.
func (s BacklogScope) Valid() bool {
	switch s {
	case "", BacklogScopeLocal, BacklogScopePR, BacklogScopePRMerge:
		return true
	}
	return false
}

// BacklogOriginKind says who filed an item.
type BacklogOriginKind string

// Backlog origin kinds.
const (
	BacklogOriginHuman        BacklogOriginKind = "human"
	BacklogOriginOrchestrator BacklogOriginKind = "orchestrator"
	BacklogOriginWorker       BacklogOriginKind = "worker"
	BacklogOriginReviewer     BacklogOriginKind = "reviewer"
)

// Valid reports whether k is a known origin kind.
func (k BacklogOriginKind) Valid() bool {
	switch k {
	case BacklogOriginHuman, BacklogOriginOrchestrator, BacklogOriginWorker, BacklogOriginReviewer:
		return true
	}
	return false
}

// BacklogLinkKind is the kind of thing a backlog link points at.
type BacklogLinkKind string

// Backlog link kinds. BacklogLinkBlockedBy is the typed item->item edge that
// orders work; BacklogLinkItem is a plain relation. Both refs are item ids.
const (
	BacklogLinkSession   BacklogLinkKind = "session"
	BacklogLinkPR        BacklogLinkKind = "pr"
	BacklogLinkIssue     BacklogLinkKind = "issue"
	BacklogLinkArtifact  BacklogLinkKind = "artifact"
	BacklogLinkMemory    BacklogLinkKind = "memory"
	BacklogLinkItem      BacklogLinkKind = "item"
	BacklogLinkBlockedBy BacklogLinkKind = "blocked_by"
)

// Valid reports whether k is a known link kind.
func (k BacklogLinkKind) Valid() bool {
	switch k {
	case BacklogLinkSession, BacklogLinkPR, BacklogLinkIssue, BacklogLinkArtifact,
		BacklogLinkMemory, BacklogLinkItem, BacklogLinkBlockedBy:
		return true
	}
	return false
}

// IsItemLink reports whether the link's ref is another backlog item's id.
func (k BacklogLinkKind) IsItemLink() bool { return k == BacklogLinkItem || k == BacklogLinkBlockedBy }

// Backlog size bounds, mirrored by the schema CHECKs.
const (
	MaxBacklogTitle         = 200
	MaxBacklogNote          = 20000
	MaxBacklogDoneWhen      = 2000
	MaxBacklogHold          = 200
	MaxBacklogAgentNotes    = 20000
	MaxBacklogExcerpt       = 1000
	MaxBacklogClosedReason  = 200
	MaxBacklogListLimit     = 500
	DefaultBacklogListLimit = 100

	// Deferred-line limits for `tallpine report --defer`.
	MaxDeferPerReport         = 3
	MaxDeferLineLength        = 200
	MaxDeferPerSession        = 30
	MaxBacklogInboxPerProject = 50
)

// Backlog domain errors.
var (
	ErrBacklogNotFound     = errors.New("backlog item not found")
	ErrBacklogInvalid      = errors.New("invalid backlog item")
	ErrBacklogHierarchy    = errors.New("backlog hierarchy violation")
	ErrBacklogCycle        = errors.New("backlog blocked_by cycle")
	ErrBacklogCrossProject = errors.New("backlog link crosses projects")
	ErrBacklogNotStartable = errors.New("backlog parent items cannot be started")
	ErrBacklogInboxCap     = errors.New("backlog inbox cap reached")
	ErrBacklogSessionCap   = errors.New("backlog per-session defer cap reached")
	ErrBacklogInvalidField = errors.New("invalid backlog field")
	ErrBacklogNotClosed    = errors.New("backlog item is not closed")
)

// BacklogOrigin records who filed an item and from what.
type BacklogOrigin struct {
	Kind      BacklogOriginKind `json:"kind"`
	SessionID string            `json:"sessionId,omitempty"`
	Excerpt   string            `json:"excerpt,omitempty"`
}

// BacklogLink is one typed reference from an item.
type BacklogLink struct {
	Kind      BacklogLinkKind `json:"kind"`
	Ref       string          `json:"ref"`
	CreatedBy string          `json:"createdBy,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
}

// BacklogItem is one deferred piece of work in a project.
type BacklogItem struct {
	ID           string
	ProjectID    ProjectID
	Seq          int64
	Title        string
	Note         string
	DoneWhen     string
	State        BacklogState
	Hold         string
	Scope        BacklogScope
	ParentID     string
	Position     int64
	Origin       BacklogOrigin
	AgentNotes   string
	Fields       map[string]string
	ClosedReason string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ClosedAt     *time.Time

	// Links is filled by single-item reads; LinkCounts by list reads.
	Links      []BacklogLink
	LinkCounts map[BacklogLinkKind]int
}

// Ref is the short, typeable per-project reference, e.g. "B12".
func (i BacklogItem) Ref() string { return FormatBacklogRef(i.Seq) }

// IsStartable reports whether work may be started directly on the item: it is
// open, unheld and not a parent. Running a parent runs its children instead.
// Blockers are not considered here (the store's next-item query does).
func (i BacklogItem) IsStartable(childCount int) bool {
	return i.State == BacklogOpen && i.Hold == "" && childCount == 0
}

// NormalizeBacklogTitle is the dedupe key: trimmed, internal whitespace
// collapsed, case-insensitive.
func NormalizeBacklogTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// Closed reasons written by automation. Anything else in closed_reason was set
// by a person or the orchestrator and is never reversed automatically.
const (
	BacklogReasonChildrenClosed = "children closed"
	BacklogReasonExpired        = "expired"
	BacklogReasonWorkerDone     = "worker done"
)

// BacklogReasonMerged is the closed_reason for an item whose PRs all merged,
// e.g. "merged #12" or "merged #12, #13".
func BacklogReasonMerged(numbers []int64) string {
	parts := make([]string, len(numbers))
	for i, n := range numbers {
		parts[i] = "#" + strconv.FormatInt(n, 10)
	}
	return "merged " + strings.Join(parts, ", ")
}
