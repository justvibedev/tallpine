package pr

import (
	"errors"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Sentinel errors returned by the PR action service.
var (
	ErrInvalidPR        = errors.New("pr: invalid identity")
	ErrPRNotFound       = errors.New("pr: not found")
	ErrPRNotMergeable   = errors.New("pr: not mergeable")
	ErrPRHeadChanged    = errors.New("pr: head changed")
	ErrPRPreconditions  = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve = errors.New("pr: nothing to resolve")
)

// PreconditionError names the merge precondition a pull request failed. It
// matches ErrPRPreconditions under errors.Is so existing callers keep working.
type PreconditionError struct {
	Blocker domain.MergeBlocker
}

func (e *PreconditionError) Error() string {
	return ErrPRPreconditions.Error() + ": " + e.Blocker.Describe()
}

func (e *PreconditionError) Unwrap() error { return ErrPRPreconditions }
