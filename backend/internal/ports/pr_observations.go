package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ErrSCMPRNotFound is the legacy PR-observation not-found sentinel. It aliases
// the provider-neutral SCM sentinel so old PRObservation callers and new SCM
// callers compose under errors.Is.
var ErrSCMPRNotFound = ErrSCMNotFound

// PRObserver fetches one legacy PR observation by canonical PR URL.
type PRObserver interface {
	Observe(ctx context.Context, prURL string) (PRObservation, error)
}

// PRObservation is what the SCM poller reports for one PR. Fetched is the
// failed-fetch guard: when false the rest is meaningless and lifecycle must not
// read it as "PR closed". Checks/Comments are observation DTOs, not persistence
// rows; the PR Manager owns mapping them into stored domain.PullRequest rows.
type PRObservation struct {
	Fetched      bool
	URL          string
	Number       int
	Title        string
	SourceBranch string
	TargetBranch string
	Draft        bool
	Merged       bool
	Closed       bool
	CI           domain.CIState
	Review       domain.ReviewDecision
	Mergeability domain.Mergeability
	// MergeableHeldByNoCI is set when Mergeability is "blocked" only because
	// the ProjectConfig.NoCI review gate held back a head the provider itself
	// reported as mergeable (see ApplyNoCIPolicy). The provider's verdict
	// still rules a conflict out.
	MergeableHeldByNoCI bool
	Checks              []PRCheckObservation
	Comments            []PRCommentObservation
}

// PRCheckObservation is one SCM check result on the observed PR.
type PRCheckObservation struct {
	Name       string
	CommitHash string
	Status     domain.PRCheckStatus
	URL        string
	LogTail    string
}

// PRCommentObservation is one review comment observed on the PR.
type PRCommentObservation struct {
	ID               string
	ThreadID         string
	ReviewID         string
	Author           string
	File             string
	Line             int
	Body             string
	URL              string
	Resolved         bool
	AutoInjectReview bool
}
