package cleanup

import (
	"sort"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Category groups what a rule cleans.
type Category string

// Categories, one per rule.
const (
	CategoryWorktrees     Category = "worktrees"
	CategoryArtifacts     Category = "artifacts"
	CategorySessionTemp   Category = "session_temp"
	CategoryHarnessCaches Category = "harness_caches"
	CategorySystemTemp    Category = "system_temp"
	CategoryBranches      Category = "branches"
	CategoryContainers    Category = "containers"
	CategoryBacklog       Category = "backlog"
	CategoryGoBuildCache  Category = "go_build_cache"
	CategoryAttachments   Category = "attachments"
)

// Pass is how thorough a run is. The hourly light pass runs the cheap rules;
// the daily full pass runs every rule.
type Pass string

// Passes.
const (
	PassLight Pass = "light"
	PassFull  Pass = "full"
)

// Valid reports whether p is a known pass.
func (p Pass) Valid() bool { return p == PassLight || p == PassFull }

// Trigger is what started a run.
type Trigger string

// Triggers.
const (
	TriggerSchedule Trigger = "schedule"
	TriggerManual   Trigger = "manual"
)

// ItemStatus is the outcome for one thing a rule looked at.
type ItemStatus string

const (
	// StatusFreed was removed (or archived) by this run.
	StatusFreed ItemStatus = "freed"
	// StatusWouldFree would be removed by a real run (dry run only).
	StatusWouldFree ItemStatus = "would_free"
	// StatusNeedsConfirmation is eligible but waits for the user to confirm.
	StatusNeedsConfirmation ItemStatus = "needs_confirmation"
	// StatusKept was deliberately left alone; Reason says why.
	StatusKept ItemStatus = "kept"
	// StatusFailed was attempted and is still in place.
	StatusFailed ItemStatus = "failed"
)

// maxItemsPerCategory bounds the items one category records per run; counts
// and byte totals still cover everything.
const maxItemsPerCategory = 50

// Item is one thing a rule looked at.
type Item struct {
	SessionID domain.SessionID `json:"sessionId,omitempty"`
	ProjectID domain.ProjectID `json:"projectId,omitempty"`
	// What names the thing in the user's terms ("worktree", "version history").
	What   string     `json:"what"`
	Path   string     `json:"path,omitempty"`
	Bytes  int64      `json:"bytes"`
	Status ItemStatus `json:"status"`
	Reason string     `json:"reason,omitempty"`
	// Attention marks a kept item whose reason the owner can act on (a
	// worktree cleanup could not salvage), not an expected wait.
	Attention bool `json:"attention,omitempty"`
}

// CategoryReport sums one category's items.
type CategoryReport struct {
	Category       Category `json:"category"`
	FreedBytes     int64    `json:"freedBytes"`
	WouldFreeBytes int64    `json:"wouldFreeBytes"`
	PendingBytes   int64    `json:"pendingBytes"`
	Freed          int      `json:"freed"`
	WouldFree      int      `json:"wouldFree"`
	Pending        int      `json:"pending"`
	Kept           int      `json:"kept"`
	Failed         int      `json:"failed"`
	Items          []Item   `json:"items"`
	ItemsTruncated bool     `json:"itemsTruncated,omitempty"`
	// Skipped is set when the category did not run (disabled, or not part of
	// this pass).
	Skipped string `json:"skipped,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (c *CategoryReport) add(item Item) {
	switch item.Status {
	case StatusFreed:
		c.Freed++
		c.FreedBytes += item.Bytes
	case StatusWouldFree:
		c.WouldFree++
		c.WouldFreeBytes += item.Bytes
	case StatusNeedsConfirmation:
		c.Pending++
		c.PendingBytes += item.Bytes
	case StatusKept:
		c.Kept++
	case StatusFailed:
		c.Failed++
	}
	c.Items = append(c.Items, item)
}

var statusRank = map[ItemStatus]int{
	StatusFreed: 0, StatusWouldFree: 0, StatusNeedsConfirmation: 1, StatusFailed: 2, StatusKept: 3,
}

// finish orders items most actionable and largest first and bounds the list.
func (c *CategoryReport) finish() {
	sort.SliceStable(c.Items, func(i, j int) bool {
		a, b := c.Items[i], c.Items[j]
		if a.Attention != b.Attention {
			return a.Attention
		}
		if statusRank[a.Status] != statusRank[b.Status] {
			return statusRank[a.Status] < statusRank[b.Status]
		}
		return a.Bytes > b.Bytes
	})
	if len(c.Items) > maxItemsPerCategory {
		c.Items = c.Items[:maxItemsPerCategory]
		c.ItemsTruncated = true
	}
	if c.Items == nil {
		c.Items = []Item{}
	}
}

// Report is one run.
type Report struct {
	ID         int64            `json:"id"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt time.Time        `json:"finishedAt"`
	Trigger    Trigger          `json:"trigger"`
	Pass       Pass             `json:"pass"`
	DryRun     bool             `json:"dryRun"`
	FreedBytes int64            `json:"freedBytes"`
	Categories []CategoryReport `json:"categories"`
	Error      string           `json:"error,omitempty"`
}

// Reclaimable sums what a run would free and what waits for confirmation.
func (r Report) Reclaimable() (wouldFree, pending int64) {
	for _, cat := range r.Categories {
		wouldFree += cat.WouldFreeBytes
		pending += cat.PendingBytes
	}
	return wouldFree, pending
}
