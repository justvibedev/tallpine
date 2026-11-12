package apitypes

// CleanupArtifactsPolicy governs the artifact directories of terminated sessions.
type CleanupArtifactsPolicy struct {
	Enabled           bool   `json:"enabled"`
	RetentionDays     int    `json:"retentionDays" minimum:"1" maximum:"3650" description:"Days after a session ended before its artifacts are cleaned."`
	AutoPrune         bool   `json:"autoPrune" description:"Remove version history and tool directories (node_modules, virtual environments, __pycache__) without asking. Off by default: they wait for confirmation."`
	DeliverableAction string `json:"deliverableAction" enum:"archive,delete" description:"What happens to deliverable files once confirmed."`
	AutoDeliverables  bool   `json:"autoDeliverables" description:"Apply deliverableAction without asking for confirmation."`
}

// CleanupBacklogPolicy governs expiry of untriaged backlog inbox items.
type CleanupBacklogPolicy struct {
	Enabled         bool `json:"enabled" description:"Drop agent-filed inbox items nobody touched for inboxExpiryDays. Dropped items stay in the archive; human-filed items never expire."`
	InboxExpiryDays int  `json:"inboxExpiryDays" minimum:"1" maximum:"3650" description:"Days an untouched agent-filed inbox item waits before it is dropped."`
}

// CleanupBranchesPolicy governs the tp/ branches of terminated worker sessions.
type CleanupBranchesPolicy struct {
	Enabled             bool `json:"enabled" description:"Delete the branches. Off by default; while off, runs only report what they would delete."`
	MergedRetentionDays int  `json:"mergedRetentionDays" minimum:"0" maximum:"3650" description:"Days after the session ended before a merged local branch is deleted."`
	StaleRetentionDays  int  `json:"staleRetentionDays" minimum:"1" maximum:"3650" description:"Days after the session ended before an unmerged local branch whose commits are all on a remote is deleted. A branch with commits that exist nowhere else is never deleted."`
	DeleteRemote        bool `json:"deleteRemote" description:"Also delete the remote branch of a merged PR while it is still at the PR's head. Off by default."`
}

// CleanupCategoryReport sums one category of a cleanup run.
type CleanupCategoryReport struct {
	Category       string        `json:"category" enum:"worktrees,artifacts,session_temp,harness_caches,system_temp,containers,branches,go_build_cache,backlog,session_retention,attachments"`
	FreedBytes     int64         `json:"freedBytes"`
	WouldFreeBytes int64         `json:"wouldFreeBytes" description:"Bytes a real run would free (dry runs)."`
	PendingBytes   int64         `json:"pendingBytes" description:"Bytes eligible but waiting for confirmation."`
	Freed          int           `json:"freed"`
	WouldFree      int           `json:"wouldFree"`
	Pending        int           `json:"pending"`
	Kept           int           `json:"kept"`
	Failed         int           `json:"failed"`
	Items          []CleanupItem `json:"items" description:"At most 50 items, most actionable and largest first."`
	ItemsTruncated bool          `json:"itemsTruncated,omitempty"`
	Skipped        string        `json:"skipped,omitempty" description:"Why the category did not run."`
	Error          string        `json:"error,omitempty"`
}

// CleanupContainersPolicy governs Docker resources labeled with an ended session.
type CleanupContainersPolicy struct {
	Enabled             bool `json:"enabled" description:"Remove the resources. Off by default; while off, runs only report what they would remove."`
	VolumeRetentionDays int  `json:"volumeRetentionDays" minimum:"0" maximum:"3650" description:"Days a session-labeled volume is kept after its session ended."`
}

// CleanupGoBuildCachePolicy governs the user-wide Go build cache.
type CleanupGoBuildCachePolicy struct {
	Enabled bool `json:"enabled" description:"Remove build cache entries unused for 2 days. Off by default: the cache is outside the data dir and shared with the user's own Go work, so runs only report what they would remove until turned on."`
}

// CleanupHarnessCachesPolicy governs per-project harness caches under the data dir.
type CleanupHarnessCachesPolicy struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retentionDays" minimum:"1" maximum:"3650" description:"Days a project must go without a live session before its caches are removed."`
}

// CleanupItem is one thing a cleanup run looked at.
type CleanupItem struct {
	SessionID string `json:"sessionId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	What      string `json:"what"`
	Path      string `json:"path,omitempty"`
	Bytes     int64  `json:"bytes"`
	Status    string `json:"status" enum:"freed,would_free,needs_confirmation,kept,failed"`
	Reason    string `json:"reason,omitempty"`
	// Attention marks a kept item the owner can act on, such as a worktree cleanup could not salvage.
	Attention bool `json:"attention,omitempty"`
}

// CleanupPolicy is the automatic cleanup configuration.
type CleanupPolicy struct {
	Automatic     bool                       `json:"automatic" description:"Run the hourly light pass and the daily full pass. Manual runs work either way."`
	Worktrees     CleanupWorktreePolicy      `json:"worktrees"`
	Artifacts     CleanupArtifactsPolicy     `json:"artifacts"`
	SessionTemp   CleanupSessionTempPolicy   `json:"sessionTemp"`
	HarnessCaches CleanupHarnessCachesPolicy `json:"harnessCaches"`
	SystemTemp    CleanupSystemTempPolicy    `json:"systemTemp"`
	GoBuildCache  CleanupGoBuildCachePolicy  `json:"goBuildCache"`
	Containers    CleanupContainersPolicy    `json:"containers"`
	Backlog       CleanupBacklogPolicy       `json:"backlog"`
	Branches      CleanupBranchesPolicy      `json:"branches"`
}

// CleanupRunReport is one cleanup run.
type CleanupRunReport struct {
	ID         int64                   `json:"id"`
	StartedAt  string                  `json:"startedAt" format:"date-time"`
	FinishedAt string                  `json:"finishedAt" format:"date-time"`
	Trigger    string                  `json:"trigger" enum:"schedule,manual"`
	Pass       string                  `json:"pass" enum:"light,full"`
	DryRun     bool                    `json:"dryRun"`
	FreedBytes int64                   `json:"freedBytes"`
	Categories []CleanupCategoryReport `json:"categories"`
	Error      string                  `json:"error,omitempty"`
}

// CleanupRunRequest starts a cleanup run.
type CleanupRunRequest struct {
	DryRun           bool   `json:"dryRun,omitempty" description:"Report what would be cleaned without removing anything."`
	Pass             string `json:"pass,omitempty" enum:"light,full" description:"Defaults to full."`
	ConfirmArtifacts bool   `json:"confirmArtifacts,omitempty" description:"The user confirmed removing the artifacts of sessions past retention: version history and tool directories, and deliverables per the policy's action."`
	// Only limits the run to these categories; the others report as skipped.
	Only []string `json:"only,omitempty" maxItems:"20" description:"Run only these categories (for example session_retention); the others report as skipped."`
}

// CleanupSessionTempPolicy governs session temp directories that outlived their session.
type CleanupSessionTempPolicy struct {
	Enabled bool `json:"enabled"`
}

// CleanupStatusResponse is the cleanup policy, the latest runs and the schedule.
type CleanupStatusResponse struct {
	Policy           CleanupPolicy     `json:"policy"`
	PolicyUpdatedAt  string            `json:"policyUpdatedAt,omitempty" format:"date-time"`
	Running          bool              `json:"running"`
	PolicyError      string            `json:"policyError,omitempty" description:"The stored policy could not be read; automatic cleanup is off until a policy is saved."`
	FirstPassPending bool              `json:"firstPassPending" description:"No scheduled pass has run yet. The first one is a dry run that only reports."`
	LastRun          *CleanupRunReport `json:"lastRun,omitempty"`
	LastDryRun       *CleanupRunReport `json:"lastDryRun,omitempty" description:"The latest dry run: what a run would reclaim, by category."`
	LastFullRunAt    string            `json:"lastFullRunAt,omitempty" format:"date-time"`
	NextLightRunAt   string            `json:"nextLightRunAt,omitempty" format:"date-time" description:"Omitted while automatic cleanup is off."`
	NextFullRunAt    string            `json:"nextFullRunAt,omitempty" format:"date-time" description:"Omitted while automatic cleanup is off."`
}

// CleanupSystemTempPolicy governs stale leftovers in the system temp directory.
type CleanupSystemTempPolicy struct {
	Enabled    bool     `json:"enabled" description:"Off by default: Tallpine cannot attribute these to a session, so it only reports them until turned on."`
	MinAgeDays int      `json:"minAgeDays" minimum:"1" maximum:"3650" description:"Only entries not modified anywhere inside for this long."`
	Patterns   []string `json:"patterns" maxItems:"50" description:"Shell globs matched against top-level entry names."`
}

// CleanupWorktreePolicy governs the worktrees of terminated sessions.
type CleanupWorktreePolicy struct {
	Enabled       bool `json:"enabled"`
	RetentionDays int  `json:"retentionDays" minimum:"0" maximum:"3650" description:"Days a terminated session's worktree is kept so a restore still finds it. A worktree with uncommitted changes, an operation in progress, stashes, or commits no other ref contains is never removed."`
	RequireMerged bool `json:"requireMerged" description:"Also keep a worktree whose commits are only on its own branch: only work merged or pushed elsewhere lets it go."`
}
