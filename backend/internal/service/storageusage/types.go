// Package storageusage measures everything Tallpine and its agents keep on
// disk and removes leftovers on request, for Settings > Storage. Measuring is
// bounded: a background refresh walks each location under a time budget,
// results are cached, and a location it could not finish is reported as a
// lower bound. Removing reuses the cleanup engine and the session manager's
// salvaging worktree cleanup; nothing here deletes a live session's data, the
// newest rollback point or an orchestrator's worktree.
package storageusage

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Category groups entries on the page.
type Category string

// Categories, in the order the page lists them.
const (
	CategoryWorktrees    Category = "worktrees"
	CategorySessionTemp  Category = "session_temp"
	CategoryArtifacts    Category = "artifacts"
	CategoryAttachments  Category = "attachments"
	CategorySalvage      Category = "salvage"
	CategoryCaches       Category = "caches"
	CategoryGoBuildCache Category = "go_build_cache"
	CategoryReleases     Category = "releases"
	CategoryReleaseBuild Category = "release_builds"
	CategoryDatabase     Category = "database"
	CategoryLogs         Category = "logs"
	CategoryExports      Category = "exports"
	CategoryBackups      Category = "backups"
	CategoryBuildCache   Category = "build_cache"
	CategoryLocalCI      Category = "local_ci"
)

// State is the owning session's state.
type State string

// States. An entry with no session has no state.
const (
	StateLive     State = "live"
	StateEnded    State = "ended"
	StateOrphaned State = "orphaned" // no session record any more
)

// Entry is one thing on disk.
type Entry struct {
	// ID names the entry for Delete: the category and the path below the
	// state root. Clients never send a path.
	ID       string           `json:"id"`
	Category Category         `json:"category"`
	Label    string           `json:"label"`
	Path     string           `json:"path"`
	Bytes    int64            `json:"bytes"`
	Files    int64            `json:"files"`
	Newest   time.Time        `json:"newest,omitempty"`
	Project  domain.ProjectID `json:"projectId,omitempty"`
	Session  domain.SessionID `json:"sessionId,omitempty"`
	State    State            `json:"state,omitempty"`
	// Partial marks a size cut short by the time budget: a lower bound.
	Partial bool `json:"partial,omitempty"`
	// Protected is why the entry cannot be removed here ("" when it can).
	Protected string `json:"protected,omitempty"`
}

// GoCache describes the machine-wide Go build cache agents share.
type GoCache struct {
	Path    string `json:"path"`
	Bytes   int64  `json:"bytes"`
	Partial bool   `json:"partial,omitempty"`
	// CapBytes is the limit scripts/go enforces (TALLPINE_GOCACHE_MAX_GB,
	// default 30 GB); 0 means no cap.
	CapBytes int64 `json:"capBytes"`
}

// Process is a process the session reaper would stop: it belongs to a session
// that has ended or no longer exists.
type Process struct {
	PID     int              `json:"pid"`
	Command string           `json:"command"`
	Session domain.SessionID `json:"sessionId"`
	Project domain.ProjectID `json:"projectId,omitempty"`
	State   State            `json:"state"`
	Started time.Time        `json:"startedAt,omitempty"`
}

// Snapshot is one measured view of the disk.
type Snapshot struct {
	ComputedAt time.Time `json:"computedAt"`
	// Refreshing is true while a newer measurement is being taken.
	Refreshing bool      `json:"refreshing"`
	TotalBytes int64     `json:"totalBytes"`
	Entries    []Entry   `json:"entries"`
	GoCache    *GoCache  `json:"goCache,omitempty"`
	Processes  []Process `json:"processes"`
}
