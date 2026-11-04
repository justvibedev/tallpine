package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// SessionIDParam is the {sessionId} path parameter shared by session routes.
type SessionIDParam struct {
	SessionID string `path:"sessionId" description:"Session identifier, e.g. project-1."`
}

// PreviewFileQuery is the query string accepted by GET
// /api/v1/sessions/{sessionId}/preview/files/*.
type PreviewFileQuery struct {
	Source string `query:"source,omitempty" enum:"workspace,artifact" description:"File root to serve from. Defaults to the session workspace; artifact selects the session artifact directory."`
	Raw    bool   `query:"raw,omitempty" description:"When true, serve Markdown files as raw source instead of rendering them to HTML for Browser preview."`
}

// PRNumberParam is the associated pull-request number in Files routes.
type PRNumberParam struct {
	PRNumber int `path:"prNumber" description:"Associated pull request number." minimum:"1"`
}

// SessionInterfaceTransitionIDParam is the {transitionId} path parameter for a
// durable interface handoff.
type SessionInterfaceTransitionIDParam struct {
	TransitionID string `path:"transitionId" description:"Durable interface-transition identifier."`
}

// ListSessionsQuery is the query string accepted by GET /api/v1/sessions.
type ListSessionsQuery struct {
	Project          string `query:"project,omitempty" description:"Project id filter."`
	Active           *bool  `query:"active,omitempty" description:"When true, return non-terminated sessions; when false, return terminated sessions."`
	OrchestratorOnly *bool  `query:"orchestratorOnly,omitempty" description:"When true, return only orchestrator sessions."`
	Fresh            *bool  `query:"fresh,omitempty" description:"When true, return only fresh non-terminated sessions."`
	Standalone       *bool  `query:"standalone,omitempty" description:"When true, return only standalone sessions (those with no project)."`
	Kind             string `query:"kind,omitempty" enum:"worker,orchestrator" description:"Session kind filter. worker keeps every session that is not an orchestrator."`
	Limit            int    `query:"limit,omitempty" minimum:"1" maximum:"200" description:"Page size. When set, sessions are ordered pinned first, then most recently updated, and nextCursor resumes the list. When omitted, every match is returned."`
	Cursor           string `query:"cursor,omitempty" description:"Opaque cursor returned by the previous page. Requires limit."`
}

// CleanupSessionsQuery is the query string accepted by POST /api/v1/sessions/cleanup.
type CleanupSessionsQuery struct {
	Project string `query:"project,omitempty" description:"Project id filter. When omitted, clean terminated sessions across all projects."`
}

// ArtifactFileQuery is the query string accepted by DELETE /api/v1/sessions/{sessionId}/artifact-files.
type ArtifactFileQuery struct {
	Path string `query:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
}

// UpdateArtifactFileRequest replaces one listed artifact file after verifying
// that the viewer's loaded copy is still current.
type UpdateArtifactFileRequest struct {
	Path                    string `json:"path" description:"Artifact-directory-relative file path, as listed in the session's artifactFiles."`
	Content                 string `json:"content" description:"The file's new UTF-8 text content."`
	ExpectedFileFingerprint string `json:"expectedFileFingerprint" description:"Hex SHA-256 of the file content the edit started from."`
}

// ArtifactFileResponse describes an artifact file after a write.
type ArtifactFileResponse struct {
	Path            string `json:"path"`
	Size            int64  `json:"size"`
	FileFingerprint string `json:"fileFingerprint" description:"Hex SHA-256 of the file's content."`
}

// WorkspaceFileQuery is the query string accepted by GET /api/v1/sessions/{sessionId}/workspace/file.
type WorkspaceFileQuery struct {
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	Path string `query:"path" description:"Session-worktree-relative file path."`
	// Section scopes the diff to one git-state section (see WorkspaceFileSections):
	// uncommitted compares the worktree against HEAD, staged compares the index
	// against HEAD, unstaged compares the worktree against the index. A file can
	// carry independent changes in both staged and unstaged. Omit (or pass
	// committed/untracked) to diff the worktree against the compare base, as
	// before this field existed.
	Section   string `query:"section,omitempty" enum:"uncommitted,committed,staged,unstaged,untracked" description:"Git-state section the file was opened from (see WorkspaceFileSections). uncommitted diffs the worktree against HEAD; staged diffs the index against HEAD; unstaged diffs the worktree against the index; omitted/committed/untracked diff the worktree against the compare base."`
	CommitSHA string `query:"commitSha,omitempty" description:"Exact commit SHA to read as an immutable committed-scope snapshot."`
}

// UpdateWorkspaceFileRequest replaces an existing text file after verifying
// that the viewer's source snapshot is still current.
type UpdateWorkspaceFileRequest struct {
	Path                    string `json:"path"`
	Content                 string `json:"content"`
	ExpectedFileFingerprint string `json:"expectedFileFingerprint"`
}

// WorkspaceFileBlobQuery is the query string accepted by GET /api/v1/sessions/{sessionId}/workspace/file/blob.
type WorkspaceFileBlobQuery struct {
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	CommitSHA string `query:"commitSha,omitempty" description:"Selected commit SHA."`
	// The handler rejects a missing path with WORKSPACE_PATH_REQUIRED, so mark it
	// required: query params carry no json tag, and requiredFromJSONTag only
	// derives `required` from those.
	Path string `query:"path" required:"true" description:"Session-worktree-relative file path."`
	Side string `query:"side,omitempty" enum:"before,after" description:"Which revision to read: the compare base (before) or the session worktree (after). Defaults to after."`
	V    string `query:"v,omitempty" description:"Cache-busting token. Ignored by the server; the response is never cached."`
}

// WorkspaceFileRevisionQuery selects one text-capable comparison side.
type WorkspaceFileRevisionQuery struct {
	Snapshot         bool   `query:"snapshot,omitempty" description:"Read the selected immutable comparison, including an empty commit range."`
	Path             string `query:"path" required:"true" description:"Session-worktree-relative file path."`
	Scope            string `query:"scope,omitempty" enum:"combined,uncommitted,committed,staged,unstaged,untracked" description:"Comparison scope. Defaults to combined."`
	Side             string `query:"side,omitempty" enum:"before,after" description:"Comparison side. Defaults to after."`
	WorkspaceVersion string `query:"workspaceVersion,omitempty" description:"Opaque workspace snapshot token used for consistency checks."`
	ExpectedRevision string `query:"expectedRevision,omitempty" description:"Opaque revision token used for optimistic consistency checks."`
	CommitSHA        string `query:"commitSha,omitempty" description:"Exact commit SHA for a committed-scope comparison. Cannot be combined with commitShas."`
	CommitSHAs       string `query:"commitShas,omitempty" description:"Comma-separated full SHAs (max 250) whose combined changes are compared. Requires the committed scope."`
	Branch           string `query:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Requires the committed scope."`
	PRNumber         int    `query:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree. Requires the committed scope."`
	SourceURL        string `query:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
	ExpectedTipSHA   string `query:"expectedTipSha,omitempty" description:"The branch or pull request view's tipSha from its history. A moved tip fails with WORKSPACE_REF_MOVED."`
	ExpectedBaseSHA  string `query:"expectedBaseSha,omitempty" description:"The branch or pull request view's baseSha from its history. A moved base fails with WORKSPACE_REF_MOVED."`
}

// WorkspaceHistoryQuery selects whose history GET
// /api/v1/sessions/{sessionId}/workspace/history reads. With neither branch
// nor prNumber it reads the checked-out worktree.
type WorkspaceHistoryQuery struct {
	Branch    string `query:"branch,omitempty" description:"Local branch to read instead of the checked-out worktree. Read from revisions only; nothing is checked out."`
	PRNumber  int    `query:"prNumber,omitempty" minimum:"1" description:"Associated pull request to read instead of the checked-out worktree."`
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the pull request, for PRs that share a number. Requires prNumber."`
}

// PRFilesQuery selects the associated pull request when multiple providers or
// repositories can have the same pull-request number.
type PRFilesQuery struct {
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
}

// PRFileQuery identifies one file in an associated pull request.
type PRFileQuery struct {
	Path         string `query:"path" required:"true" description:"Repository-relative file path."`
	PreviousPath string `query:"previousPath,omitempty" description:"Previous repository-relative path supplied by the selected PR file summary for rename detection."`
	SourceURL    string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
	CommitSHA    string `query:"commitSha,omitempty" description:"Exact SHA of one of the pull request's commits; reads that commit's change instead of the whole pull request."`
}

// PRFileRevisionQuery selects one immutable side of a pull-request comparison.
type PRFileRevisionQuery struct {
	Path      string `query:"path" required:"true" description:"Repository-relative file path."`
	Side      string `query:"side,omitempty" enum:"before,after" description:"Comparison side. Defaults to after."`
	SourceURL string `query:"sourceUrl,omitempty" description:"Stable URL of the selected associated pull request."`
	CommitSHA string `query:"commitSha,omitempty" description:"Exact SHA of one of the pull request's commits; before is its first parent, after is the commit."`
}

// WorkspaceSearchQuery is the query string accepted by the workspace path search.
type WorkspaceSearchQuery struct {
	Query  string `query:"query" required:"true" description:"Case-insensitive path substring."`
	Cursor string `query:"cursor,omitempty" description:"Opaque pagination cursor returned by the previous page."`
	Limit  int    `query:"limit,omitempty" minimum:"1" maximum:"100" description:"Maximum results. Defaults to 50."`
}

// WorkspaceResolveQuery is the query string accepted by GET /api/v1/sessions/{sessionId}/workspace/resolve.
type WorkspaceResolveQuery struct {
	Path string `query:"path" required:"true" description:"Absolute or workspace-relative file path to classify."`
}

// WorkspaceTreeQuery is the query string accepted by GET /api/v1/sessions/{sessionId}/workspace/tree.
type WorkspaceTreeQuery struct {
	Filter          string `query:"filter,omitempty" description:"Case-insensitive path search across the selected snapshot."`
	CommitSHA       string `query:"commitSha,omitempty" description:"Selected commit SHA."`
	Snapshot        bool   `query:"snapshot,omitempty" description:"Read the immutable review snapshot instead of the live worktree."`
	Branch          string `query:"branch,omitempty" description:"Local or remote-tracking branch to read without checkout."`
	PRNumber        int    `query:"prNumber,omitempty" description:"Associated pull request number."`
	SourceURL       string `query:"sourceUrl,omitempty" description:"Associated pull request URL."`
	ExpectedTipSHA  string `query:"expectedTipSha,omitempty" description:"Pinned source tip for snapshot reads. Must remain reachable from the selected source."`
	ExpectedBaseSHA string `query:"expectedBaseSha,omitempty" description:"Pinned comparison base for snapshot reads."`
	CommitSHAs      string `query:"commitShas,omitempty" description:"Comma-separated selected commit SHAs."`

	Path string `query:"path,omitempty" description:"Directory path relative to the session workspace root. Empty or omitted lists the root."`
}

// ListWorkspaceTreeResponse is the body of GET /api/v1/sessions/{sessionId}/workspace/tree.
// Unlike ListWorkspaceFilesResponse (every changed file, whole worktree), this
// is one directory level of the full worktree — tracked and
// untracked-but-not-ignored — for lazily expanding a file explorer.
type ListWorkspaceTreeResponse struct {
	Snapshot  bool                 `json:"snapshot,omitempty" description:"True only when the tree was read from the requested immutable snapshot."`
	SessionID domain.SessionID     `json:"sessionId"`
	Path      string               `json:"path"`
	Entries   []WorkspaceTreeEntry `json:"entries"`
	Truncated bool                 `json:"truncated"`
}

// WorkspaceTreeEntry is one immediate child of a listed directory.
type WorkspaceTreeEntry struct {
	Name string                            `json:"name"`
	Path string                            `json:"path"`
	Type sessionsvc.WorkspaceTreeEntryType `json:"type" enum:"file,dir"`
	// Status is set for files only; omitted for directories.
	Status sessionsvc.WorkspaceFileStatus `json:"status,omitempty" enum:"unmodified,modified,added,deleted,renamed"`
	// HasChanges is set for directories only: true when a descendant file is
	// non-unmodified, so a collapsed folder can still show it contains changes.
	HasChanges bool  `json:"hasChanges,omitempty"`
	Size       int64 `json:"size,omitempty"`
	Binary     bool  `json:"binary,omitempty"`
}
