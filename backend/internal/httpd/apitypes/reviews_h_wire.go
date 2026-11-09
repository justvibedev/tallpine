package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// ListReviewsResponse is the body of GET /api/v1/sessions/{sessionId}/reviews.
// reviewerHandleId is the live reviewer pane's runtime handle, for the UI to
// attach its terminal over /mux (empty when no reviewer has run).
type ListReviewsResponse struct {
	ReviewerHandleID      string                 `json:"reviewerHandleId"`
	ReviewerHarness       domain.ReviewerHarness `json:"reviewerHarness,omitempty"`
	ReviewerActivityState string                 `json:"reviewerActivityState,omitempty" enum:"active,idle,waiting_input,blocked,exited"`
	Reviews               []domain.PRReviewState `json:"reviews"`
	// Runs is every recorded pass for this session, newest first. Reviews only
	// carries the current and previous run per PR, which cannot answer "what did
	// the other reviewer say" once a third pass has run — so the client cannot
	// show one summary across reviewers without this.
	Runs            []domain.ReviewRun      `json:"runs"`
	ReviewerSurface *domain.ReviewerSurface `json:"reviewerSurface,omitempty"`
	// ActiveReviewers is every reviewer with a live pane or a running pass,
	// selected reviewer first. Several reviewers can work on one worker at once.
	ActiveReviewers []domain.ReviewerSurface `json:"activeReviewers"`
	// QueuedReviews are passes waiting for the reviewer to be free; none has a
	// run yet. `review cancel` withdraws them.
	QueuedReviews []domain.QueuedReviewTrigger `json:"queuedReviews"`
}

// TriggerReviewResponse is the body of trigger (200/201). reviews carries the
// PR-scoped review state after the trigger.
type TriggerReviewResponse struct {
	ReviewerHandleID string                  `json:"reviewerHandleId"`
	Reviews          []domain.PRReviewState  `json:"reviews"`
	Runs             []domain.ReviewRun      `json:"runs"`
	ReviewerSurface  *domain.ReviewerSurface `json:"reviewerSurface,omitempty"`
	// Created is true when a new review pass was started (HTTP 201) and false
	// when an existing run for the same commit was reused (HTTP 200).
	Created bool `json:"created" description:"True when a new review pass was started; false when an existing run for the same commit was reused."`
	// Queued is true when the reviewer was busy and the pass was parked to start
	// once it is free (HTTP 200, nothing created yet).
	Queued bool `json:"queued" description:"True when the reviewer was busy on another head and the pass was queued to start once it is free; nothing was created yet."`
	// AutoInjectEnabled is true when this request turned the worker session's
	// review auto-inject on.
	AutoInjectEnabled bool `json:"autoInjectEnabled" description:"True when this request turned the worker session's review auto-inject on."`
}

// ReviewRunResponse is the body of submit (200). It carries the run plus the
// reviewer pane handle so the UI can attach a terminal.
type ReviewRunResponse struct {
	Review           domain.ReviewRun   `json:"review"`
	Reviews          []domain.ReviewRun `json:"reviews"`
	ReviewerHandleID string             `json:"reviewerHandleId"`
}

// SubmitReviewInput is the body of POST /api/v1/sessions/{sessionId}/reviews/submit.
type SubmitReviewInput struct {
	RunID          string             `json:"runId,omitempty" description:"Review run id being completed."`
	Verdict        string             `json:"verdict,omitempty" description:"Review verdict: approved or changes_requested."`
	Body           string             `json:"body,omitempty" description:"Review body recorded by Tallpine. Required for changes_requested."`
	GithubReviewID string             `json:"githubReviewId,omitempty" description:"Id of the GitHub PR review the reviewer posted, if any."`
	Reviews        []SubmitReviewItem `json:"reviews,omitempty" description:"Batched review results recorded by one reviewer CLI command."`
}

// SubmitReviewItem is one review result in a batched submit request.
type SubmitReviewItem struct {
	RunID          string `json:"runId" description:"Review run id being completed."`
	Verdict        string `json:"verdict" description:"Review verdict: approved or changes_requested."`
	Body           string `json:"body,omitempty" description:"Review body recorded by Tallpine. Required for changes_requested."`
	GithubReviewID string `json:"githubReviewId,omitempty" description:"Id of the GitHub PR review the reviewer posted, if any."`
}
