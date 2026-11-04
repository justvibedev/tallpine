package controllers

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// ListWorkspaceFilesResponse is the body of GET /api/v1/sessions/{sessionId}/workspace/files.
type ListWorkspaceFilesResponse struct {
	SessionID        domain.SessionID                `json:"sessionId"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	Files            []WorkspaceFileSummary          `json:"files"`
	Truncated        bool                            `json:"truncated"`
	// Sections groups the same working tree into git-state sections. Only
	// populated for single-repo sessions; empty for workspace-project
	// (multi-repo) and scratch sessions.
	Sections WorkspaceFileSections `json:"sections"`
	// Commits are the commits between the compare base and HEAD, newest first.
	Commits          []WorkspaceCommitSummary `json:"commits"`
	CommitsTruncated bool                     `json:"commitsTruncated,omitempty" description:"True when older commits were left out of commits: the list keeps the newest 250, and stops at the last commit whose changes fit the daemon's size cap."`
	Summary          WorkspaceSummary         `json:"summary"`
	// Degraded indicates that the primary file list is available but optional
	// Git-state enrichment failed and can be retried.
	Degraded     bool   `json:"degraded"`
	DegradedCode string `json:"degradedCode,omitempty"`
	// Ahead and Behind are omitted when no push/pull data is available (no
	// upstream, detached HEAD).
	Ahead  *int `json:"ahead,omitempty"`
	Behind *int `json:"behind,omitempty"`
}

// WorkspaceManifestResponse is the compact, latency-sensitive response used
// for the initial Changes paint. Complete repository inventory and commit
// history remain on their dedicated/legacy routes.
type WorkspaceManifestResponse struct {
	SessionID        domain.SessionID                `json:"sessionId"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	Files            []WorkspaceFileSummary          `json:"files"`
	Sections         WorkspaceFileSections           `json:"sections"`
	Summary          WorkspaceSummary                `json:"summary"`
	Truncated        bool                            `json:"truncated"`
	Stale            bool                            `json:"stale"`
	Refreshing       bool                            `json:"refreshing"`
	Degraded         bool                            `json:"degraded"`
	DegradedCode     string                          `json:"degradedCode,omitempty"`
}

// WorkspaceHistoryResponse is lazy commit/upstream metadata for the review
// header. It is intentionally separate from the first-paint manifest.
type WorkspaceHistoryResponse struct {
	SessionID        domain.SessionID         `json:"sessionId"`
	View             WorkspaceViewResponse    `json:"view"`
	Commits          []WorkspaceCommitSummary `json:"commits"`
	CommitsTruncated bool                     `json:"commitsTruncated,omitempty"`
	RangeSummary     *WorkspaceSummary        `json:"rangeSummary,omitempty" description:"Totals for All commits. Views only."`
	Ahead            *int                     `json:"ahead,omitempty" description:"Checked-out view only."`
	Behind           *int                     `json:"behind,omitempty" description:"Checked-out view only."`
	Landed           bool                     `json:"landed,omitempty" description:"Checked-out view only. True when HEAD has moved past the session's creation commit and is already contained in the project base ref (local refs only). A display and cleanup hint; it never changes cleanup behavior."`
}

// WorkspaceViewResponse describes whose history a workspace read covers.
type WorkspaceViewResponse struct {
	Kind      sessionsvc.WorkspaceViewKind  `json:"kind" enum:"checked_out,branch,pull_request"`
	Name      string                        `json:"name,omitempty" description:"Branch name, or the pull request's source branch."`
	TipSHA    string                        `json:"tipSha,omitempty" description:"Fence a later view read with it as expectedTipSha."`
	BaseSHA   string                        `json:"baseSha,omitempty" description:"Set only in since_base mode. Fence a later view read with it as expectedBaseSha."`
	BaseRef   string                        `json:"baseRef,omitempty"`
	RangeMode sessionsvc.WorkspaceRangeMode `json:"rangeMode" enum:"since_base,recent" description:"since_base lists the commits since baseSha; recent lists the newest 50 first-parent commits of a default branch, or of a branch with no default branch to measure from."`
}

// WorkspaceBranchesResponse lists the branches a session's review can read.
type WorkspaceBranchesResponse struct {
	SessionID     domain.SessionID          `json:"sessionId"`
	Supported     bool                      `json:"supported" description:"False for scratch sessions, which have no Git history."`
	DefaultBranch string                    `json:"defaultBranch,omitempty"`
	Branches      []WorkspaceBranchResponse `json:"branches" description:"The checked-out row first, then the rest newest first; at most 200 rows."`
	Truncated     bool                      `json:"truncated"`
}

// WorkspaceBranchResponse is one branch a review can read. A local branch
// with a pull request is only annotated with it; a pull request whose source
// branch is not local gets a row of its own.
type WorkspaceBranchResponse struct {
	Kind       sessionsvc.WorkspaceBranchKind `json:"kind" enum:"local,remote,pull_request" description:"local reads as a branch view (or the checked-out worktree); pull_request reads as a pull request view."`
	Name       string                         `json:"name" description:"Branch name, the pull request's source branch, or empty for a detached HEAD."`
	TipSHA     string                         `json:"tipSha,omitempty"`
	UpdatedAt  *time.Time                     `json:"updatedAt,omitempty" description:"The tip's committer time, or the pull request's last update."`
	CheckedOut bool                           `json:"checkedOut"`
	Detached   bool                           `json:"detached,omitempty"`
	IsDefault  bool                           `json:"isDefault"`
	PRNumber   int                            `json:"prNumber,omitempty"`
	PRURL      string                         `json:"prUrl,omitempty" description:"Pass as sourceUrl with prNumber to read a pull_request row."`
	PRTitle    string                         `json:"prTitle,omitempty"`
}

// ListPRFilesResponse is the exact base...head changed-file set for one PR.
type ListPRFilesResponse struct {
	SessionID domain.SessionID       `json:"sessionId"`
	Files     []WorkspaceFileSummary `json:"files"`
	// Commits are the pull request's own commits (base..head), newest first.
	// File sizes are not read for commit files.
	Commits          []WorkspaceCommitSummary `json:"commits"`
	CommitsTruncated bool                     `json:"commitsTruncated,omitempty" description:"True when older commits were left out of commits: the list keeps the newest 250, and stops at the last commit whose changes fit the daemon's size cap."`
	Truncated        bool                     `json:"truncated"`
	Summary          WorkspaceSummary         `json:"summary"`
}

// WorkspaceFileSections groups a session workspace's changed files by git
// state: uncommitted (worktree vs HEAD, untracked files included), staged
// (index vs HEAD), unstaged (worktree vs index), untracked, and committed
// (HEAD vs the compare base). A partially staged file can appear in both
// staged and unstaged, but only once in uncommitted.
type WorkspaceFileSections struct {
	Uncommitted []WorkspaceFileSummary `json:"uncommitted" description:"Worktree vs HEAD, untracked files included. Always present; a response without it comes from an older daemon."`
	Staged      []WorkspaceFileSummary `json:"staged"`
	Unstaged    []WorkspaceFileSummary `json:"unstaged"`
	Untracked   []WorkspaceFileSummary `json:"untracked"`
	Committed   []WorkspaceFileSummary `json:"committed"`
}

// WorkspaceCommitSummary is one commit between the compare base and HEAD.
type WorkspaceCommitSummary struct {
	SHA       string                 `json:"sha"`
	Subject   string                 `json:"subject"`
	Author    string                 `json:"author"`
	Timestamp time.Time              `json:"timestamp"`
	Files     []WorkspaceFileSummary `json:"files"`
}

// WorkspaceSummary aggregates a session workspace's base..worktree diff into
// totals for the Files panel header.
type WorkspaceSummary struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

// WorkspaceFileSummary is one file row in the session workspace browser.
type WorkspaceFileSummary struct {
	Path            string                         `json:"path"`
	PreviousPath    string                         `json:"previousPath,omitempty"`
	Status          sessionsvc.WorkspaceFileStatus `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Additions       int                            `json:"additions"`
	Deletions       int                            `json:"deletions"`
	Size            int64                          `json:"size"`
	Binary          bool                           `json:"binary"`
	Editable        bool                           `json:"editable"`
	FileFingerprint string                         `json:"fileFingerprint"`
}

// WorkspaceFileResponse is the body of GET /api/v1/sessions/{sessionId}/workspace/file.
type WorkspaceFileResponse struct {
	Historical       bool                            `json:"historical,omitempty" description:"True only for a read-only revision, never a live worktree file."`
	SessionID        domain.SessionID                `json:"sessionId"`
	Path             string                          `json:"path"`
	PreviousPath     string                          `json:"previousPath,omitempty"`
	Status           sessionsvc.WorkspaceFileStatus  `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Additions        int                             `json:"additions"`
	Deletions        int                             `json:"deletions"`
	Size             int64                           `json:"size"`
	Binary           bool                            `json:"binary"`
	Deleted          bool                            `json:"deleted"`
	Editable         bool                            `json:"editable"`
	ImageMediaType   string                          `json:"imageMediaType,omitempty"`
	Content          string                          `json:"content"`
	ContentTruncated bool                            `json:"contentTruncated"`
	Diff             string                          `json:"diff"`
	DiffTruncated    bool                            `json:"diffTruncated"`
	CompareBaseSHA   string                          `json:"compareBaseSha,omitempty"`
	CompareBaseRef   string                          `json:"compareBaseRef,omitempty"`
	CompareMode      sessionsvc.WorkspaceCompareMode `json:"compareMode,omitempty" enum:"base,head_fallback"`
	WorkspaceVersion string                          `json:"workspaceVersion"`
	FileFingerprint  string                          `json:"fileFingerprint"`
}

// WorkspaceDiffRequest requests renderer-independent unified patches.
type WorkspaceDiffRequest struct {
	Scope            string   `json:"scope" enum:"combined,uncommitted,committed,staged,unstaged,untracked"`
	Paths            []string `json:"paths" minItems:"1" maxItems:"100"`
	ContextLines     int      `json:"contextLines" minimum:"0" maximum:"20"`
	IgnoreWhitespace bool     `json:"ignoreWhitespace"`
	WorkspaceVersion string   `json:"workspaceVersion,omitempty"`
	CommitSHA        string   `json:"commitSha,omitempty" description:"Exact commit SHA for a committed-scope comparison. Cannot be combined with commitShas."`
	CommitSHAs       []string `json:"commitShas,omitempty" maxItems:"250" description:"Full SHAs whose combined changes are compared. Requires the committed scope."`
	Branch           string   `json:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Requires the committed scope."`
	PRNumber         int      `json:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree. Requires the committed scope."`
	SourceURL        string   `json:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string   `json:"expectedTipSha,omitempty" description:"The view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string   `json:"expectedBaseSha,omitempty" description:"The view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
}

// WorkspaceCommitSelectionRequest asks for the combined changed files of
// ticked commits in a view, or of its whole range.
type WorkspaceCommitSelectionRequest struct {
	Branch           string   `json:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree."`
	PRNumber         int      `json:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree."`
	SourceURL        string   `json:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string   `json:"expectedTipSha,omitempty" description:"The view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string   `json:"expectedBaseSha,omitempty" description:"The view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
	CommitSHAs       []string `json:"commitShas" maxItems:"250" description:"Full SHAs. Empty = the whole range (All commits)."`
	WorkspaceVersion string   `json:"workspaceVersion,omitempty" description:"Fence for checked-out selections."`
}

// WorkspaceCommitSelectionResponse is the combined changed-file set of a
// commit selection. Commits that cannot be combined are not an error: the
// response has combinable false, no files, and a conflict.
type WorkspaceCommitSelectionResponse struct {
	SessionID  domain.SessionID                 `json:"sessionId"`
	TipSHA     string                           `json:"tipSha"`
	BaseSHA    string                           `json:"baseSha,omitempty"`
	Contiguous bool                             `json:"contiguous" description:"True when the commits form one unbroken range."`
	Combinable bool                             `json:"combinable"`
	Conflict   *WorkspaceCommitConflictResponse `json:"conflict,omitempty"`
	Files      []WorkspaceFileSummary           `json:"files"`
	Summary    WorkspaceSummary                 `json:"summary"`
	Truncated  bool                             `json:"truncated"`
}

// WorkspaceCommitConflictResponse names why ticked commits cannot be combined.
type WorkspaceCommitConflictResponse struct {
	CommitSHA string                                   `json:"commitSha" description:"The ticked commit whose changes did not apply."`
	Paths     []string                                 `json:"paths" description:"Up to 20 paths that did not apply."`
	Reason    sessionsvc.WorkspaceCommitConflictReason `json:"reason" enum:"conflict,unsupported_history"`
}

// WorkspaceDiffDeferredResponse describes a file omitted from an initial patch.
type WorkspaceDiffDeferredResponse struct {
	Path   string `json:"path"`
	Reason string `json:"reason" enum:"binary,oversized,generated,long_line,budget_exceeded"`
}

// WorkspaceDiffGroupResponse is one repository-local grouped patch.
type WorkspaceDiffGroupResponse struct {
	Repository    string                          `json:"repository,omitempty"`
	Patch         string                          `json:"patch"`
	Truncated     bool                            `json:"truncated"`
	IncludedPaths []string                        `json:"includedPaths"`
	Deferred      []WorkspaceDiffDeferredResponse `json:"deferred"`
	Errors        []WorkspaceDiffErrorResponse    `json:"errors"`
}

// WorkspaceDiffErrorResponse is a redacted repository-local patch failure.
type WorkspaceDiffErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WorkspaceDiffsResponse returns renderer-independent patches for one snapshot.
type WorkspaceDiffsResponse struct {
	SessionID        domain.SessionID             `json:"sessionId"`
	WorkspaceVersion string                       `json:"workspaceVersion"`
	Groups           []WorkspaceDiffGroupResponse `json:"groups"`
}

// WorkspaceFileRevisionResponse returns one bounded side of a file comparison.
type WorkspaceFileRevisionResponse struct {
	SessionID        domain.SessionID                 `json:"sessionId"`
	Path             string                           `json:"path"`
	Side             sessionsvc.WorkspaceFileBlobSide `json:"side" enum:"before,after"`
	Revision         string                           `json:"revision,omitempty"`
	WorkspaceVersion string                           `json:"workspaceVersion"`
	MediaType        string                           `json:"mediaType,omitempty"`
	Encoding         string                           `json:"encoding,omitempty"`
	Size             int64                            `json:"size"`
	Exists           bool                             `json:"exists"`
	Binary           bool                             `json:"binary"`
	Truncated        bool                             `json:"truncated"`
	Content          string                           `json:"content"`
}

// WorkspaceFileSearchResultResponse is one path search match.
type WorkspaceFileSearchResultResponse struct {
	Path            string                         `json:"path"`
	Status          sessionsvc.WorkspaceFileStatus `json:"status" enum:"unmodified,modified,added,deleted,renamed"`
	Size            int64                          `json:"size"`
	Binary          bool                           `json:"binary"`
	FileFingerprint string                         `json:"fileFingerprint"`
}

// WorkspaceFileSearchResponse is a bounded page of workspace path matches.
type WorkspaceFileSearchResponse struct {
	SessionID  domain.SessionID                    `json:"sessionId"`
	Query      string                              `json:"query"`
	Results    []WorkspaceFileSearchResultResponse `json:"results"`
	NextCursor string                              `json:"nextCursor,omitempty"`
	Truncated  bool                                `json:"truncated"`
}

// WorkspacePathResolutionResponse is the body of GET /api/v1/sessions/{sessionId}/workspace/resolve.
// Path is relative to the root named by Kind and empty when Kind is outside;
// the response never carries a local absolute path.
type WorkspacePathResolutionResponse struct {
	SessionID domain.SessionID             `json:"sessionId"`
	Kind      sessionsvc.WorkspacePathKind `json:"kind" enum:"workspace,artifact,outside"`
	Path      string                       `json:"path"`
}

// DesktopWorkspaceLocationResponse is returned only by the LAN-blocked desktop
// handoff route. Electron main consumes the absolute path and never exposes it
// through the preload bridge.
type DesktopWorkspaceLocationResponse struct {
	SessionID     domain.SessionID `json:"sessionId"`
	WorkspacePath string           `json:"workspacePath"`
}

// DesktopArtifactLocationResponse is returned only by the LAN-blocked desktop
// artifact route. Electron main consumes the absolute path and never exposes it
// through the preload bridge.
type DesktopArtifactLocationResponse struct {
	SessionID   domain.SessionID `json:"sessionId"`
	ArtifactDir string           `json:"artifactDir"`
}

// SessionPreviewResponse is the body of GET /api/v1/sessions/{sessionId}/preview.
type SessionPreviewResponse struct {
	SessionID  domain.SessionID `json:"sessionId"`
	PreviewURL string           `json:"previewUrl,omitempty"`
	Entry      string           `json:"entry,omitempty"`
	Source     string           `json:"source,omitempty" enum:"workspace,artifact" description:"Root containing the selected static entry; omitted for external app URLs."`
}
