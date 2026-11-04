package controllers

import (
	"time"

	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

// NewSessionPRSummary maps the service PR summary model to its HTTP DTO.
func NewSessionPRSummary(in sessionsvc.PRSummary) SessionPRSummary {
	return SessionPRSummary{
		URL:              in.URL,
		HTMLURL:          in.HTMLURL,
		Number:           in.Number,
		Title:            in.Title,
		State:            in.State,
		Provider:         in.Provider,
		Repo:             in.Repo,
		Author:           in.Author,
		AuthorAvatarURL:  in.AuthorAvatarURL,
		SourceBranch:     in.SourceBranch,
		TargetBranch:     in.TargetBranch,
		HeadSHA:          in.HeadSHA,
		Additions:        in.Additions,
		Deletions:        in.Deletions,
		ChangedFiles:     in.ChangedFiles,
		CI:               newSessionPRCISummary(in.CI),
		Review:           newSessionPRReviewSummary(in.Review),
		Mergeability:     newSessionPRMergeabilitySummary(in.Mergeability),
		StateChangedAt:   optionalTime(in.StateChangedAt),
		CreatedAt:        optionalTime(in.CreatedAt),
		UpdatedAt:        in.UpdatedAt,
		ObservedAt:       in.ObservedAt,
		CIObservedAt:     in.CIObservedAt,
		ReviewObservedAt: in.ReviewObservedAt,
	}
}

func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func newSessionPRCISummary(in sessionsvc.PRCISummary) SessionPRCISummary {
	checks := make([]SessionPRFailingCheck, 0, len(in.FailingChecks))
	for _, ch := range in.FailingChecks {
		checks = append(checks, SessionPRFailingCheck{Name: ch.Name, Status: ch.Status, Conclusion: ch.Conclusion, URL: ch.URL})
	}
	return SessionPRCISummary{State: in.State, FailingChecks: checks, AutoInjectCI: in.AutoInjectCI}
}

func newSessionPRReviewSummary(in sessionsvc.PRReviewSummary) SessionPRReviewSummary {
	reviewers := newSessionPRCommentReviewers(in.UnresolvedBy)
	resolvedReviewers := newSessionPRCommentReviewers(in.ResolvedBy)
	entries := make([]SessionPRReviewEntry, 0, len(in.Reviews))
	for _, review := range in.Reviews {
		entries = append(entries, SessionPRReviewEntry{
			ReviewerID:       review.Reviewer,
			Verdict:          review.Verdict,
			Body:             review.Body,
			ReviewURL:        review.URL,
			SubmittedAt:      review.SubmittedAt,
			IsBot:            review.IsBot,
			AutoInjectReview: review.AutoInjectReview,
		})
	}
	return SessionPRReviewSummary{
		Decision:                   in.Decision,
		HasUnresolvedHumanComments: in.HasUnresolvedHumanComments,
		UnresolvedThreadCount:      in.UnresolvedThreadCount,
		UnresolvedBy:               reviewers,
		ResolvedBy:                 resolvedReviewers,
		Reviews:                    entries,
	}
}

func newSessionPRCommentReviewers(in []sessionsvc.PRUnresolvedReviewer) []SessionPRUnresolvedReviewer {
	reviewers := make([]SessionPRUnresolvedReviewer, 0, len(in))
	for _, reviewer := range in {
		links := make([]SessionPRReviewCommentLink, 0, len(reviewer.Links))
		for _, link := range reviewer.Links {
			links = append(links, SessionPRReviewCommentLink{URL: link.URL, ReviewID: link.ReviewID, File: link.File, Line: link.Line, Body: link.Body, AutoInjectReview: link.AutoInjectReview})
		}
		reviewers = append(reviewers, SessionPRUnresolvedReviewer{ReviewerID: reviewer.ReviewerID, Count: reviewer.Count, Links: links, ReviewURL: reviewer.ReviewURL, IsBot: reviewer.IsBot})
	}
	return reviewers
}

func newSessionPRMergeabilitySummary(in sessionsvc.PRMergeabilitySummary) SessionPRMergeabilitySummary {
	files := make([]SessionPRConflictFile, 0, len(in.ConflictFiles))
	for _, file := range in.ConflictFiles {
		files = append(files, SessionPRConflictFile{Path: file.Path, URL: file.URL})
	}
	// No reasons is an empty array, never null: clients read it as a list.
	reasons := append([]string{}, in.Reasons...)
	return SessionPRMergeabilitySummary{State: in.State, Reasons: reasons, PRURL: in.PRURL, ConflictFiles: files}
}
