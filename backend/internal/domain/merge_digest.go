package domain

import "time"

// MergeDigestPR is one pull request as the cross-lane merge digest sees it.
type MergeDigestPR struct {
	URL     string
	Number  int
	State   PRState
	Title   string
	HeadSHA string
	// MergeCommitSHA is the merge commit of a merged PR ("" when unknown).
	MergeCommitSHA string
	SessionID      SessionID
	// LaneItemID is the lane of the owning session ("" = un-laned).
	LaneItemID string
	UpdatedAt  time.Time
	// MergedAt is set for merged PRs.
	MergedAt time.Time
	// Paths are the changed files as last observed; the provider caps the list.
	Paths []string
}
