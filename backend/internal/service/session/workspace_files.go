package session

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/hookutil"
)

const (
	maxWorkspaceFiles     = 5000
	maxWorkspaceFileBytes = 256 * 1024
	maxWorkspaceDiffBytes = 512 * 1024
	// Tallpine's agent adapters write this marker into self-ignoring .gitignore
	// files beside their workspace-local control files. Scratch workspaces have
	// no Git index to hide that infrastructure from the review surfaces, so the
	// filesystem readers honor the same ownership marker directly.
	tallpineManagedGitignoreSentinel = hookutil.GitignoreSentinel
	// Copilot profiles are ignored through .git/info/exclude in project
	// worktrees, but standalone workspaces have no Git metadata. The profile
	// carries its own ownership marker so scratch readers can hide it directly.
	tallpineManagedCopilotProfileSentinel = hookutil.CopilotAgentProfileSentinel
	// maxWorkspaceImageBytes caps a single image revision streamed to the diff
	// viewer. Anything larger is refused rather than buffered.
	maxWorkspaceImageBytes = 16 * 1024 * 1024
	// maxCommitLogCommits caps a Commits menu to its newest commits. GitHub's
	// pull request commits API stops at the same 250.
	maxCommitLogCommits = 250
	// maxCommitLogBytes caps each of a Commits menu's two git log passes, so a
	// huge commit cannot become an unbounded in-memory buffer.
	maxCommitLogBytes = 2 * 1024 * 1024
)

// WorkspaceFileStatus describes a session-worktree file relative to its compare base.
type WorkspaceFileStatus string

// Workspace file status values reported by the session workspace browser.
const (
	WorkspaceFileUnmodified WorkspaceFileStatus = "unmodified"
	WorkspaceFileModified   WorkspaceFileStatus = "modified"
	WorkspaceFileAdded      WorkspaceFileStatus = "added"
	WorkspaceFileDeleted    WorkspaceFileStatus = "deleted"
	WorkspaceFileRenamed    WorkspaceFileStatus = "renamed"
)

// WorkspaceCompareMode describes the Git revision used for workspace diffs.
type WorkspaceCompareMode string

const (
	// WorkspaceCompareBase means diffs are against the session's recorded base.
	WorkspaceCompareBase WorkspaceCompareMode = "base"
	// WorkspaceCompareHeadFallback means Tallpine could not resolve a base and used the
	// previous HEAD-only behavior.
	WorkspaceCompareHeadFallback WorkspaceCompareMode = "head_fallback"
)

// WorkspaceFiles is the read model for the session workspace file browser.
type WorkspaceFiles struct {
	SessionID        domain.SessionID
	WorkspaceVersion string
	CompareBaseSHA   string
	CompareBaseRef   string
	CompareMode      WorkspaceCompareMode
	Files            []WorkspaceFileSummary
	Truncated        bool
	// Sections splits the same working tree into git-state groups (staged,
	// unstaged, untracked, committed-since-base), independent of the
	// base..worktree Files list above. Only populated for single-repo
	// sessions; workspace-project (multi-repo) and scratch sessions fill only
	// Uncommitted.
	Sections WorkspaceFileSections
	// Commits are the commits between the compare base and HEAD, newest first.
	Commits []CommitSummary
	// CommitsTruncated means older commits were left out of Commits.
	CommitsTruncated bool
	// Summary aggregates Files (excluding unmodified entries) into totals for
	// the panel header.
	Summary WorkspaceSummary
	// Degraded means the primary file list was returned but optional Git state
	// enrichment could not be collected. Callers should keep the file list and
	// offer a retry for sections/commit metadata.
	Degraded     bool
	DegradedCode string
	// Ahead and Behind are commit counts against the branch's upstream (or
	// origin/<branch> when no upstream is configured). Nil when neither can
	// be resolved — that means "no push/pull data," not an error.
	Ahead  *int
	Behind *int
}

// WorkspaceFileSections groups the session worktree's changed files by git
// state: uncommitted (worktree vs HEAD, untracked files included), staged
// (index vs HEAD), unstaged (worktree vs index), untracked, and committed
// (HEAD vs the compare base). A partially staged file can appear in both
// Staged and Unstaged, but only once in Uncommitted.
type WorkspaceFileSections struct {
	Uncommitted []WorkspaceFileSummary
	Staged      []WorkspaceFileSummary
	Unstaged    []WorkspaceFileSummary
	Untracked   []WorkspaceFileSummary
	Committed   []WorkspaceFileSummary
}

// WorkspaceFileSection identifies which of WorkspaceFileSections a
// GetWorkspaceFile request's diff should be scoped to. A partially staged
// file has independent staged and unstaged diffs, so the caller must say
// which one it opened.
type WorkspaceFileSection string

// Git-state sections a workspace file row can belong to.
const (
	WorkspaceFileSectionUncommitted WorkspaceFileSection = "uncommitted"
	WorkspaceFileSectionStaged      WorkspaceFileSection = "staged"
	WorkspaceFileSectionUnstaged    WorkspaceFileSection = "unstaged"
	WorkspaceFileSectionUntracked   WorkspaceFileSection = "untracked"
	WorkspaceFileSectionCommitted   WorkspaceFileSection = "committed"
)

// CommitSummary is one commit between the compare base and HEAD.
type CommitSummary struct {
	SHA       string
	Subject   string
	Author    string
	Timestamp time.Time
	Files     []WorkspaceFileSummary
}

// WorkspaceSummary aggregates the base..worktree diff into totals.
type WorkspaceSummary struct {
	Files     int
	Additions int
	Deletions int
}

// WorkspaceManifest is the latency-sensitive workspace review read model.
// Unlike WorkspaceFiles it contains only changed files and omits complete
// repository inventory and commit-history enrichment. WorkspaceVersion is
// deliberately identical to the compatible WorkspaceFiles snapshot so the
// existing detail, revision, and batch-diff endpoints can fence requests
// against either read model during migration.
type WorkspaceManifest struct {
	SessionID        domain.SessionID
	WorkspaceVersion string
	CompareBaseSHA   string
	CompareBaseRef   string
	CompareMode      WorkspaceCompareMode
	Files            []WorkspaceFileSummary
	Sections         WorkspaceFileSections
	Summary          WorkspaceSummary
	Truncated        bool
	Stale            bool
	Refreshing       bool
	Degraded         bool
	DegradedCode     string
}

// WorkspaceHistory is the non-critical commit metadata loaded after the
// latency-sensitive manifest. It deliberately avoids enumerating every file
// in the repository.
type WorkspaceHistory struct {
	SessionID domain.SessionID
	// View describes whose history this is: the checked-out worktree, another
	// local branch, or a pull request that is not checked out.
	View             WorkspaceViewInfo
	Commits          []CommitSummary
	CommitsTruncated bool
	// RangeSummary totals the view's whole range (All commits). Only branch and
	// pull request views fill it; the checked-out view sums its committed
	// section instead.
	RangeSummary *WorkspaceSummary
	// Ahead and Behind are only filled for the checked-out view.
	Ahead  *int
	Behind *int
	// Landed is only filled for the checked-out view: HEAD moved past the
	// session's creation commit and is fully contained in the project base ref
	// (local refs only). Display and cleanup hint only.
	Landed bool
}

// WorkspaceFileSummary is one file row in the session workspace browser.
type WorkspaceFileSummary struct {
	Path            string
	PreviousPath    string
	Status          WorkspaceFileStatus
	Additions       int
	Deletions       int
	Size            int64
	Binary          bool
	Editable        bool
	FileFingerprint string
}

// WorkspaceFileDetail is the selected file's current content and diff.
type WorkspaceFileDetail struct {
	SessionID          domain.SessionID
	Path               string
	PreviousPath       string
	Status             WorkspaceFileStatus
	Additions          int
	Deletions          int
	Size               int64
	Binary             bool
	Deleted            bool
	Editable           bool
	ImageMediaType     string
	Content            string
	ContentTruncated   bool
	Diff               string
	DiffTruncated      bool
	WorkspaceTruncated bool
	CompareBaseSHA     string
	CompareBaseRef     string
	CompareMode        WorkspaceCompareMode
	WorkspaceVersion   string
	FileFingerprint    string
	Historical         bool
	fileModTime        time.Time
}

// UpdateWorkspaceFileInput replaces one existing text file. The fingerprint is
// required so a UI edit cannot silently overwrite a newer agent-authored copy.
type UpdateWorkspaceFileInput struct {
	Path                    string
	Content                 string
	ExpectedFileFingerprint string
}
