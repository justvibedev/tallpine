package store

import (
	"database/sql"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

// ---- domain <-> gen mapping ----

func genPRParams(r domain.PullRequest) gen.UpsertPRParams {
	return gen.UpsertPRParams{
		URL:                      r.URL,
		SessionID:                r.SessionID,
		Number:                   int64(r.Number),
		PRState:                  r.State(),
		ReviewDecision:           reviewOrDefault(r.Review),
		CIState:                  ciOrDefault(r.CI),
		Mergeability:             mergeabilityOrDefault(r.Mergeability),
		UpdatedAt:                r.UpdatedAt,
		StateChangedAt:           nullTime(initialPRStateChangedAt(r)),
		Provider:                 r.Provider,
		Host:                     r.Host,
		Repo:                     r.Repo,
		ProviderID:               r.ProviderID,
		SourceBranch:             r.SourceBranch,
		TargetBranch:             r.TargetBranch,
		HeadSha:                  r.HeadSHA,
		Title:                    r.Title,
		Additions:                int64(r.Additions),
		Deletions:                int64(r.Deletions),
		ChangedFiles:             int64(r.ChangedFiles),
		Author:                   r.Author,
		AuthorAvatarURL:          r.AuthorAvatarURL,
		BaseSha:                  r.BaseSHA,
		MergeCommitSha:           r.MergeCommitSHA,
		IsDraft:                  boolInt(r.Draft),
		IsMerged:                 boolInt(r.Merged),
		IsClosed:                 boolInt(r.Closed),
		ProviderState:            r.ProviderState,
		ProviderMergeable:        r.ProviderMergeable,
		ProviderMergeStateStatus: r.ProviderMergeStateStatus,
		HtmlURL:                  r.HTMLURL,
		CreatedAtProvider:        nullTime(r.CreatedAtProvider),
		UpdatedAtProvider:        nullTime(r.UpdatedAtProvider),
		MergedAtProvider:         nullTime(r.MergedAtProvider),
		ClosedAtProvider:         nullTime(r.ClosedAtProvider),
		MetadataHash:             r.MetadataHash,
		CIHash:                   r.CIHash,
		ReviewHash:               r.ReviewHash,
		ObservedAt:               nullTime(r.ObservedAt),
		CIObservedAt:             nullTime(r.CIObservedAt),
		ReviewObservedAt:         nullTime(r.ReviewObservedAt),
		ReviewPartial:            r.ReviewPartial,
		ID:                       r.SessionID,
	}
}

func genLegacyPRParams(r domain.PullRequest) gen.UpsertLegacyPRParams {
	return gen.UpsertLegacyPRParams{
		URL:              r.URL,
		SessionID:        r.SessionID,
		Number:           int64(r.Number),
		PRState:          r.State(),
		ReviewDecision:   reviewOrDefault(r.Review),
		CIState:          ciOrDefault(r.CI),
		Mergeability:     mergeabilityOrDefault(r.Mergeability),
		UpdatedAt:        r.UpdatedAt,
		StateChangedAt:   nullTime(initialPRStateChangedAt(r)),
		IsDraft:          boolInt(r.Draft),
		IsMerged:         boolInt(r.Merged),
		IsClosed:         boolInt(r.Closed),
		ReviewObservedAt: nullTime(r.ReviewObservedAt),
		ReviewPartial:    r.ReviewPartial,
		ID:               r.SessionID,
	}
}

func reviewOrDefault(v domain.ReviewDecision) domain.ReviewDecision {
	if v == "" {
		return domain.ReviewNone
	}
	return v
}

func ciOrDefault(v domain.CIState) domain.CIState {
	if v == "" {
		return domain.CIUnknown
	}
	return v
}

func mergeabilityOrDefault(v domain.Mergeability) domain.Mergeability {
	if v == "" {
		return domain.MergeUnknown
	}
	return v
}

func initialPRStateChangedAt(r domain.PullRequest) time.Time {
	if !r.StateChangedAt.IsZero() {
		return r.StateChangedAt
	}
	switch r.State() {
	case domain.PRStateMerged:
		return r.MergedAtProvider
	case domain.PRStateClosed:
		return r.ClosedAtProvider
	case domain.PRStateDraft, domain.PRStateOpen:
		return r.CreatedAtProvider
	default:
		return time.Time{}
	}
}

func prRowFromGen(p gen.PR) domain.PullRequest {
	return domain.PullRequest{
		URL:                      p.URL,
		SessionID:                p.SessionID,
		Number:                   int(p.Number),
		Draft:                    p.PRState == domain.PRStateDraft || p.IsDraft != 0,
		Merged:                   p.PRState == domain.PRStateMerged || p.IsMerged != 0,
		Closed:                   p.PRState == domain.PRStateClosed || p.IsClosed != 0,
		CI:                       p.CIState,
		Review:                   p.ReviewDecision,
		Mergeability:             p.Mergeability,
		UpdatedAt:                p.UpdatedAt,
		StateChangedAt:           timeFromNull(p.StateChangedAt),
		Provider:                 p.Provider,
		Host:                     p.Host,
		Repo:                     p.Repo,
		ProviderID:               p.ProviderID,
		SourceBranch:             p.SourceBranch,
		TargetBranch:             p.TargetBranch,
		HeadSHA:                  p.HeadSha,
		Title:                    p.Title,
		Additions:                int(p.Additions),
		Deletions:                int(p.Deletions),
		ChangedFiles:             int(p.ChangedFiles),
		Author:                   p.Author,
		AuthorAvatarURL:          p.AuthorAvatarURL,
		BaseSHA:                  p.BaseSha,
		MergeCommitSHA:           p.MergeCommitSha,
		ProviderState:            p.ProviderState,
		ProviderMergeable:        p.ProviderMergeable,
		ProviderMergeStateStatus: p.ProviderMergeStateStatus,
		HTMLURL:                  p.HtmlURL,
		CreatedAtProvider:        timeFromNull(p.CreatedAtProvider),
		UpdatedAtProvider:        timeFromNull(p.UpdatedAtProvider),
		MergedAtProvider:         timeFromNull(p.MergedAtProvider),
		ClosedAtProvider:         timeFromNull(p.ClosedAtProvider),
		MetadataHash:             p.MetadataHash,
		CIHash:                   p.CIHash,
		ReviewHash:               p.ReviewHash,
		ObservedAt:               timeFromNull(p.ObservedAt),
		CIObservedAt:             timeFromNull(p.CIObservedAt),
		ReviewObservedAt:         timeFromNull(p.ReviewObservedAt),
		ReviewPartial:            p.ReviewPartial,
		AutoInjectCI:             p.AutoInjectCI,
	}
}

func genCheckParams(prURL string, c domain.PullRequestCheck) gen.UpsertPRCheckParams {
	status := c.Status
	if status == "" {
		status = domain.PRCheckUnknown
	}
	return gen.UpsertPRCheckParams{
		PRURL: prURL, Name: c.Name, CommitHash: c.CommitHash,
		Status: status, URL: c.URL, LogTail: c.LogTail, CreatedAt: c.CreatedAt,
		Conclusion: c.Conclusion, Details: c.Details,
	}
}

func checkRowFromGen(c gen.PRCheck) domain.PullRequestCheck {
	return domain.PullRequestCheck{
		Name: c.Name, CommitHash: c.CommitHash, Status: c.Status,
		Conclusion: c.Conclusion, URL: c.URL, Details: c.Details,
		LogTail: c.LogTail, CreatedAt: c.CreatedAt,
	}
}

func genCommentParams(prURL string, c domain.PullRequestComment) gen.UpsertPRCommentParams {
	return gen.UpsertPRCommentParams{
		PRURL: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
		ThreadID: c.ThreadID, ReviewID: c.ReviewID, URL: c.URL, IsBot: boolInt(c.IsBot), AutoInjectReview: c.AutoInjectReview,
	}
}

func genLegacyCommentParams(prURL string, c domain.PullRequestComment) gen.InsertLegacyPRCommentParams {
	return gen.InsertLegacyPRCommentParams{
		PRURL: prURL, CommentID: c.ID, Author: c.Author, File: c.File,
		Line: int64(c.Line), Body: c.Body, Resolved: c.Resolved, CreatedAt: c.CreatedAt,
		ThreadID: "", ReviewID: c.ReviewID, URL: "", IsBot: 0,
	}
}

func commentFromGen(c gen.PRComment) domain.PullRequestComment {
	return domain.PullRequestComment{
		ThreadID: c.ThreadID, ReviewID: c.ReviewID, ID: c.CommentID, Author: c.Author,
		File: c.File, Line: int(c.Line), Body: c.Body, URL: c.URL,
		Resolved: c.Resolved, IsBot: c.IsBot != 0, CreatedAt: c.CreatedAt,
		AutoInjectReview: c.AutoInjectReview,
	}
}

func genReviewThreadParams(prURL string, th domain.PullRequestReviewThread) gen.UpsertPRReviewThreadParams {
	return gen.UpsertPRReviewThreadParams{
		PRURL: prURL, ThreadID: th.ThreadID, Path: th.Path,
		Line: int64(th.Line), Resolved: boolInt(th.Resolved),
		IsBot: boolInt(th.IsBot), SemanticHash: th.SemanticHash,
		UpdatedAt: th.UpdatedAt,
	}
}

func reviewThreadFromGen(th gen.PRReviewThread) domain.PullRequestReviewThread {
	return domain.PullRequestReviewThread{
		ThreadID: th.ThreadID, Path: th.Path, Line: int(th.Line),
		Resolved: th.Resolved != 0, IsBot: th.IsBot != 0,
		SemanticHash: th.SemanticHash, UpdatedAt: th.UpdatedAt,
	}
}

func genReviewParams(prURL string, review domain.PullRequestReview) gen.UpsertPRReviewParams {
	id := review.ID
	if id == "" {
		id = review.URL
	}
	return gen.UpsertPRReviewParams{
		PRURL:            prURL,
		ReviewID:         id,
		Author:           review.Author,
		State:            string(reviewOrDefault(review.State)),
		URL:              review.URL,
		IsBot:            boolInt(review.IsBot),
		SubmittedAt:      review.SubmittedAt,
		Body:             review.Body,
		TargetSha:        review.TargetSHA,
		AutoInjectReview: review.AutoInjectReview,
	}
}

func reviewFromGen(review gen.PRReview) domain.PullRequestReview {
	return domain.PullRequestReview{
		ID:               review.ReviewID,
		Author:           review.Author,
		State:            domain.ReviewDecision(review.State),
		URL:              review.URL,
		Body:             review.Body,
		IsBot:            review.IsBot != 0,
		TargetSHA:        review.TargetSha,
		SubmittedAt:      review.SubmittedAt,
		AutoInjectReview: review.AutoInjectReview,
	}
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func timeFromNull(t sql.NullTime) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}
