package controllers

// ResolveReviewCommentRequest is the body of POST /api/v1/sessions/{sessionId}/reviews/comments/resolve.
type ResolveReviewCommentRequest struct {
	PullRequestURL string `json:"pullRequestUrl,omitempty" description:"Tracked pull request URL. Required when the session has multiple PRs."`
	CommentURL     string `json:"commentUrl" description:"Provider URL of the unresolved review comment to resolve."`
}

// ResolveReviewCommentResponse is returned after Tallpine resolves a provider review thread.
type ResolveReviewCommentResponse struct {
	OK bool `json:"ok"`
}

// RequestRereviewRequest is the body of POST /api/v1/sessions/{sessionId}/reviews/rerequest.
type RequestRereviewRequest struct {
	PullRequestURL string `json:"pullRequestUrl,omitempty" description:"Tracked pull request URL. Required when the session has multiple PRs."`
	ReviewerID     string `json:"reviewerId" description:"Provider login of the reviewer to ask for another review."`
}

// RequestRereviewResponse is returned after Tallpine asks the SCM provider for another review.
type RequestRereviewResponse struct {
	OK bool `json:"ok"`
}
