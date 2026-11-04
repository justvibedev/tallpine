package controllers

// ArtifactStorageQuery documents GET /artifacts/storage.
type ArtifactStorageQuery struct {
	OlderThanDays int `query:"olderThanDays" minimum:"1" maximum:"3650" description:"Age window for retention candidates, in days. Defaults to 30."`
}

// ArtifactStorageSession is one session's artifact disk usage.
type ArtifactStorageSession struct {
	SessionID     string `json:"sessionId"`
	ProjectID     string `json:"projectId,omitempty"`
	DisplayName   string `json:"displayName,omitempty"`
	Terminated    bool   `json:"terminated"`
	UpdatedAt     string `json:"updatedAt" format:"date-time"`
	SizeBytes     int64  `json:"sizeBytes"`
	VersionsBytes int64  `json:"versionsBytes" description:"Part of sizeBytes held by the version history."`
	Files         int    `json:"files"`
	OverWarn      bool   `json:"overWarn" description:"The session's artifacts are at or above warnBytes."`
	Eligible      bool   `json:"eligible" description:"The session is terminated and older than the requested window and no open backlog item references it, so a retention action may apply to it."`
	Protected     bool   `json:"protected" description:"An open backlog item links this session or names a path in its directory, so retention skips it."`
}

// ArtifactStorageResponse is the artifact disk usage across sessions, largest first.
type ArtifactStorageResponse struct {
	OlderThanDays int                      `json:"olderThanDays"`
	WarnBytes     int64                    `json:"warnBytes"`
	TotalBytes    int64                    `json:"totalBytes"`
	VersionsBytes int64                    `json:"versionsBytes"`
	EligibleBytes int64                    `json:"eligibleBytes"`
	Sessions      []ArtifactStorageSession `json:"sessions"`
}

// ArtifactRetentionRequest archives or deletes the artifacts of long-terminated sessions.
type ArtifactRetentionRequest struct {
	Action        string   `json:"action" enum:"archive,delete" description:"archive keeps deliverable files as a compressed archive and removes the rest; delete removes everything."`
	OlderThanDays int      `json:"olderThanDays" minimum:"1" maximum:"3650"`
	SessionIDs    []string `json:"sessionIds" minItems:"1"`
	Confirm       bool     `json:"confirm" description:"Must be true: the user confirmed the action."`
}

// ArtifactRetentionResult is the outcome for one session.
type ArtifactRetentionResult struct {
	SessionID   string `json:"sessionId"`
	Status      string `json:"status" enum:"archived,deleted,skipped,failed"`
	Reason      string `json:"reason,omitempty"`
	FreedBytes  int64  `json:"freedBytes"`
	ArchivePath string `json:"archivePath,omitempty"`
}

// ArtifactRetentionResponse lists the per-session outcomes.
type ArtifactRetentionResponse struct {
	FreedBytes int64                     `json:"freedBytes"`
	Results    []ArtifactRetentionResult `json:"results"`
}

// UpdateCleanupBranchesPolicy changes branch cleanup; omitted fields are kept.
type UpdateCleanupBranchesPolicy struct {
	Enabled             *bool `json:"enabled,omitempty"`
	MergedRetentionDays *int  `json:"mergedRetentionDays,omitempty" minimum:"0" maximum:"3650"`
	StaleRetentionDays  *int  `json:"staleRetentionDays,omitempty" minimum:"1" maximum:"3650"`
	DeleteRemote        *bool `json:"deleteRemote,omitempty"`
}

// UpdateCleanupBacklogPolicy changes backlog expiry; omitted fields are kept.
type UpdateCleanupBacklogPolicy struct {
	Enabled         *bool `json:"enabled,omitempty"`
	InboxExpiryDays *int  `json:"inboxExpiryDays,omitempty" minimum:"1" maximum:"3650"`
}

// UpdateCleanupGoBuildCachePolicy changes Go build cache cleanup; omitted fields are kept.
type UpdateCleanupGoBuildCachePolicy struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// UpdateCleanupContainersPolicy changes container cleanup; omitted fields are kept.
type UpdateCleanupContainersPolicy struct {
	Enabled             *bool `json:"enabled,omitempty"`
	VolumeRetentionDays *int  `json:"volumeRetentionDays,omitempty" minimum:"0" maximum:"3650"`
}

// UpdateCleanupWorktreePolicy changes worktree cleanup; omitted fields are kept.
type UpdateCleanupWorktreePolicy struct {
	Enabled       *bool `json:"enabled,omitempty"`
	RetentionDays *int  `json:"retentionDays,omitempty" minimum:"0" maximum:"3650"`
	RequireMerged *bool `json:"requireMerged,omitempty"`
}

// UpdateCleanupArtifactsPolicy changes artifact cleanup; omitted fields are kept.
type UpdateCleanupArtifactsPolicy struct {
	Enabled           *bool   `json:"enabled,omitempty"`
	RetentionDays     *int    `json:"retentionDays,omitempty" minimum:"1" maximum:"3650"`
	AutoPrune         *bool   `json:"autoPrune,omitempty"`
	DeliverableAction *string `json:"deliverableAction,omitempty" enum:"archive,delete"`
	AutoDeliverables  *bool   `json:"autoDeliverables,omitempty"`
}

// UpdateCleanupSessionTempPolicy changes session temp cleanup.
type UpdateCleanupSessionTempPolicy struct {
	Enabled *bool `json:"enabled,omitempty"`
}

// UpdateCleanupHarnessCachesPolicy changes harness cache cleanup; omitted fields are kept.
type UpdateCleanupHarnessCachesPolicy struct {
	Enabled       *bool `json:"enabled,omitempty"`
	RetentionDays *int  `json:"retentionDays,omitempty" minimum:"1" maximum:"3650"`
}

// UpdateCleanupSystemTempPolicy changes system temp cleanup; omitted fields are kept.
type UpdateCleanupSystemTempPolicy struct {
	Enabled    *bool     `json:"enabled,omitempty"`
	MinAgeDays *int      `json:"minAgeDays,omitempty" minimum:"1" maximum:"3650"`
	Patterns   *[]string `json:"patterns,omitempty" maxItems:"50" description:"Replaces the whole list."`
}

// UpdateCleanupPolicyRequest changes the cleanup policy. Only the fields sent
// change; unknown fields are refused.
type UpdateCleanupPolicyRequest struct {
	Automatic     *bool                             `json:"automatic,omitempty"`
	Worktrees     *UpdateCleanupWorktreePolicy      `json:"worktrees,omitempty"`
	Artifacts     *UpdateCleanupArtifactsPolicy     `json:"artifacts,omitempty"`
	SessionTemp   *UpdateCleanupSessionTempPolicy   `json:"sessionTemp,omitempty"`
	HarnessCaches *UpdateCleanupHarnessCachesPolicy `json:"harnessCaches,omitempty"`
	SystemTemp    *UpdateCleanupSystemTempPolicy    `json:"systemTemp,omitempty"`
	GoBuildCache  *UpdateCleanupGoBuildCachePolicy  `json:"goBuildCache,omitempty"`
	Branches      *UpdateCleanupBranchesPolicy      `json:"branches,omitempty"`
	Containers    *UpdateCleanupContainersPolicy    `json:"containers,omitempty"`
	Backlog       *UpdateCleanupBacklogPolicy       `json:"backlog,omitempty"`
}

// CleanupRunsQuery documents GET /cleanup/runs.
type CleanupRunsQuery struct {
	Limit          int  `query:"limit" minimum:"1" maximum:"200" description:"Runs to return, newest first. Defaults to 20."`
	IncludeDryRuns bool `query:"includeDryRuns" description:"Include dry runs."`
}

// CleanupRunsResponse lists recent cleanup runs, newest first.
type CleanupRunsResponse struct {
	Runs []CleanupRunReport `json:"runs"`
}
