package controllers

// StorageQuery documents GET /storage.
type StorageQuery struct {
	Refresh bool `query:"refresh" description:"Start a new measurement now instead of serving the cached one."`
}

// StorageEntry is one thing Tallpine or its agents keep on disk.
type StorageEntry struct {
	ID        string `json:"id" description:"Names the entry for removal; stable across measurements."`
	Category  string `json:"category" enum:"worktrees,session_temp,artifacts,attachments,salvage,caches,go_build_cache,releases,release_builds,database,logs,exports,backups,build_cache,local_ci"`
	Label     string `json:"label"`
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes" description:"Space the entry occupies on disk (as du reports it)."`
	Files     int64  `json:"files"`
	NewestAt  string `json:"newestAt,omitempty" description:"Most recent modification inside the entry; its age is how long nothing touched it."`
	ProjectID string `json:"projectId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	State     string `json:"state,omitempty" enum:"live,ended,orphaned" description:"The owning session's state; orphaned means no session record exists."`
	Partial   bool   `json:"partial,omitempty" description:"The measurement ran out of time: bytes is a lower bound."`
	Protected string `json:"protected,omitempty" description:"Why the entry cannot be removed here; empty when it can."`
}

// StorageGoCache describes the Go build cache agents share.
type StorageGoCache struct {
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	Partial  bool   `json:"partial,omitempty"`
	CapBytes int64  `json:"capBytes" description:"The limit scripts/go enforces on the cache; 0 means no cap."`
}

// StorageProcess is a process the session reaper would stop.
type StorageProcess struct {
	PID       int    `json:"pid"`
	Command   string `json:"command"`
	SessionID string `json:"sessionId"`
	ProjectID string `json:"projectId,omitempty"`
	State     string `json:"state" enum:"ended,orphaned"`
	StartedAt string `json:"startedAt,omitempty"`
}

// StorageResponse is one measured view of the disk.
type StorageResponse struct {
	ComputedAt string           `json:"computedAt,omitempty"`
	Refreshing bool             `json:"refreshing" description:"A newer measurement is being taken; poll again."`
	TotalBytes int64            `json:"totalBytes"`
	Entries    []StorageEntry   `json:"entries"`
	GoCache    *StorageGoCache  `json:"goCache,omitempty"`
	Processes  []StorageProcess `json:"processes"`
}

// StorageDeleteRequest removes one entry.
type StorageDeleteRequest struct {
	ID string `json:"id" minLength:"1"`
}

// StorageDeleteResponse acknowledges a removal.
type StorageDeleteResponse struct {
	Removed string `json:"removed"`
}

// StorageCleanupRequest cleans up everything safe.
type StorageCleanupRequest struct {
	DryRun bool `json:"dryRun,omitempty" description:"Report what would be removed without removing anything."`
}
