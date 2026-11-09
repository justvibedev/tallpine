package pr

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ActionManager is the controller-facing contract for guarded PR mutations.
type ActionManager interface {
	Merge(ctx context.Context, request MergeRequest) (MergeResult, error)
	ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error)
}

// MergeRequest identifies the tracked PR and pins the mutation to the exact
// head the user saw when they clicked Merge.
type MergeRequest struct {
	PRID            string
	PRURL           string
	ExpectedHeadSHA string
	// DryRun stops after the precondition checks, before any merge.
	DryRun bool
	// Requester is the scoped agent session making the request; empty for the
	// app or CLI user.
	Requester domain.SessionID
}

// MergeResult is the successful outcome of a PR merge.
type MergeResult struct {
	PRNumber       int
	Method         string
	State          domain.PRState
	MergeCommitSHA string
	StateChangedAt time.Time
	// DryRun is true when the preconditions passed and nothing was merged.
	DryRun bool
}

// ResolveResult is the successful outcome of a resolve-comments operation.
type ResolveResult struct {
	Resolved int
}
