package session

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// latestReviewSummaries returns each reviewer's newest submitted review.
// Providers normalize non-decisive COMMENTED reviews as ReviewNone; a
// ReviewRequired value is an aggregate PR decision, not a submitted review.
func latestReviewSummaries(reviews []domain.PullRequestReview) map[string]domain.PullRequestReview {
	latestByReviewer := map[string]domain.PullRequestReview{}
	for _, review := range reviews {
		if review.State == domain.ReviewRequired {
			continue
		}
		reviewer := strings.TrimSpace(review.Author)
		if reviewer == "" {
			reviewer = "unknown"
		}
		current, ok := latestByReviewer[reviewer]
		if !ok || reviewAfter(review, current) {
			latestByReviewer[reviewer] = review
		}
	}
	return latestByReviewer
}

func unresolvedHumanThreadCount(threads []domain.PullRequestReviewThread) int {
	count := 0
	for _, thread := range threads {
		if !thread.Resolved && !thread.IsBot {
			count++
		}
	}
	return count
}

// dedupeReviewThreads collapses provider thread rows that describe the same
// thread under different PR URL aliases of one canonical PR. Thread identity is
// the provider thread id; a row without one keeps its own identity. When alias
// records disagree, the most recently updated row wins, and an unresolved row
// wins a tie so a stale alias cannot silently hide active feedback.
func dedupeReviewThreads(threads []domain.PullRequestReviewThread) []domain.PullRequestReviewThread {
	type identity struct {
		threadID string
		fallback int
	}
	winner := map[identity]domain.PullRequestReviewThread{}
	order := []identity{}
	for i, thread := range threads {
		var id identity
		if thread.ThreadID == "" {
			// No provider identity: count the row as its own thread.
			id = identity{fallback: i}
			winner[id] = thread
			order = append(order, id)
			continue
		}
		id = identity{threadID: thread.ThreadID}
		current, ok := winner[id]
		if !ok {
			order = append(order, id)
		}
		if !ok || threadAfter(thread, current) {
			winner[id] = thread
		}
	}
	out := make([]domain.PullRequestReviewThread, 0, len(order))
	for _, id := range order {
		out = append(out, winner[id])
	}
	return out
}

// threadAfter reports whether a is the fresher of two records of the same
// provider thread; an unresolved record wins an exact tie.
func threadAfter(a, b domain.PullRequestReviewThread) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return !a.Resolved && b.Resolved
}

// latestDecisiveReviews returns each reviewer's most recent decisive review —
// the latest one whose verdict is approved or changes_requested.
func latestDecisiveReviews(reviews []domain.PullRequestReview) map[string]domain.PullRequestReview {
	latestByReviewer := map[string]domain.PullRequestReview{}
	for _, review := range reviews {
		if review.State != domain.ReviewChangesRequest && review.State != domain.ReviewApproved {
			continue
		}
		reviewer := strings.TrimSpace(review.Author)
		if reviewer == "" {
			reviewer = "unknown"
		}
		current, ok := latestByReviewer[reviewer]
		if !ok || reviewAfter(review, current) {
			latestByReviewer[reviewer] = review
		}
	}
	return latestByReviewer
}

// latestChangesRequestedReviews narrows latestDecisiveReviews to reviewers whose
// latest decisive verdict is changes_requested — used to attach a review link to
// the unresolved-comment grouping.
func latestChangesRequestedReviews(reviews []domain.PullRequestReview) map[string]domain.PullRequestReview {
	out := map[string]domain.PullRequestReview{}
	for reviewer, review := range latestDecisiveReviews(reviews) {
		if review.State == domain.ReviewChangesRequest {
			out[reviewer] = review
		}
	}
	return out
}

func reviewAfter(a, b domain.PullRequestReview) bool {
	if a.SubmittedAt.IsZero() || b.SubmittedAt.IsZero() {
		return a.SubmittedAt.IsZero() == b.SubmittedAt.IsZero() && a.ID > b.ID
	}
	if a.SubmittedAt.Equal(b.SubmittedAt) {
		return a.ID > b.ID
	}
	return a.SubmittedAt.After(b.SubmittedAt)
}
