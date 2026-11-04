package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/gen"
)

func writePRRows(ctx context.Context, q *gen.Queries, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode, replaceLegacyComments, rejectReassignment bool) error {
	pr = normalizedPRIdentity(pr)
	var err error
	pr, err = resolveWeakPRAlias(ctx, q, pr)
	if err != nil {
		return err
	}
	if replaceLegacyComments {
		if rejectReassignment {
			existing, err := q.GetPR(ctx, pr.URL)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if err == nil && existing.SessionID != pr.SessionID {
				return fmt.Errorf("pr %s already belongs to session %s", pr.URL, existing.SessionID)
			}
		}
		if err := q.UpsertLegacyPR(ctx, genLegacyPRParams(pr)); err != nil {
			return err
		}
	} else {
		if err := upsertPRWithIdentity(ctx, q, pr, rejectReassignment); err != nil {
			return err
		}
	}
	if pr.Paths != nil {
		if err := q.DeletePRPaths(ctx, pr.URL); err != nil {
			return err
		}
		for _, path := range pr.Paths {
			if err := q.InsertPRPath(ctx, gen.InsertPRPathParams{PRURL: pr.URL, Path: path}); err != nil {
				return err
			}
		}
	}
	for _, c := range checks {
		if err := q.UpsertPRCheck(ctx, genCheckParams(pr.URL, c)); err != nil {
			return err
		}
	}
	if replaceLegacyComments {
		if err := q.DeleteLegacyPRComments(ctx, pr.URL); err != nil {
			return err
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, review := range reviews {
			if err := q.UpsertPRReview(ctx, genReviewParams(pr.URL, review)); err != nil {
				return fmt.Errorf("review %q: %w", review.ID, err)
			}
		}
	}
	// Preserve reviews that are still present so their original injection
	// decision survives the upsert. Only reviews no longer returned by the SCM
	// are removed in replace mode.
	if reviewMode == ports.ReviewWriteReplace {
		observed := make(map[string]struct{}, len(reviews))
		for _, review := range reviews {
			observed[review.ID] = struct{}{}
		}
		existing, err := q.ListPRReviews(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list reviews for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.ReviewID]; ok {
				continue
			}
			if err := q.DeletePRReview(ctx, gen.DeletePRReviewParams{PRURL: pr.URL, ReviewID: row.ReviewID}); err != nil {
				return fmt.Errorf("prune review %q: %w", row.ReviewID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, th := range threads {
			if err := q.UpsertPRReviewThread(ctx, genReviewThreadParams(pr.URL, th)); err != nil {
				return fmt.Errorf("review thread %q: %w", th.ThreadID, err)
			}
		}
	}
	// Replace mode prunes orphans (threads no longer observed in the upstream
	// listing) AFTER the upserts above, so that threads present in both the
	// pre- and post-state hit ON CONFLICT DO UPDATE and fire the UPDATE trigger
	// (e.g. pr_review_thread_resolved when resolved flips). The old
	// delete-everything-first approach made every poll look like a fresh INSERT
	// and the UPDATE trigger was unreachable for the common Replace path.
	if reviewMode == ports.ReviewWriteReplace {
		observed := make(map[string]struct{}, len(threads))
		for _, th := range threads {
			observed[th.ThreadID] = struct{}{}
		}
		existing, err := q.ListPRReviewThreads(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list review threads for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.ThreadID]; ok {
				continue
			}
			if err := q.DeletePRReviewThread(ctx, gen.DeletePRReviewThreadParams{PRURL: pr.URL, ThreadID: row.ThreadID}); err != nil {
				return fmt.Errorf("prune review thread %q: %w", row.ThreadID, err)
			}
		}
	}
	if reviewMode == ports.ReviewWriteReplace || reviewMode == ports.ReviewWriteMerge {
		for _, c := range comments {
			if err := q.UpsertPRComment(ctx, genCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("comment %q: %w", c.ID, err)
			}
		}
		observed := make(map[string]struct{}, len(comments))
		for _, c := range comments {
			observed[c.ID] = struct{}{}
		}
		touchedThreads := map[string]struct{}{}
		if reviewMode == ports.ReviewWriteMerge {
			for _, threadID := range reviewThreadIDs(threads, comments) {
				touchedThreads[threadID] = struct{}{}
			}
		}
		existing, err := q.ListPRComments(ctx, pr.URL)
		if err != nil {
			return fmt.Errorf("list comments for prune %s: %w", pr.URL, err)
		}
		for _, row := range existing {
			if _, ok := observed[row.CommentID]; ok {
				continue
			}
			if reviewMode == ports.ReviewWriteMerge {
				if _, touched := touchedThreads[row.ThreadID]; !touched {
					continue
				}
			}
			if err := q.DeletePRComment(ctx, gen.DeletePRCommentParams{PRURL: pr.URL, CommentID: row.CommentID}); err != nil {
				return fmt.Errorf("prune comment %q: %w", row.CommentID, err)
			}
		}
	} else if replaceLegacyComments {
		for _, c := range comments {
			if err := q.InsertLegacyPRComment(ctx, genLegacyCommentParams(pr.URL, c)); err != nil {
				return fmt.Errorf("legacy comment %q: %w", c.ID, err)
			}
		}
	}
	return nil
}

func normalizedPRIdentity(pr domain.PullRequest) domain.PullRequest {
	pr.Provider = strings.ToLower(strings.TrimSpace(pr.Provider))
	pr.Host = strings.ToLower(strings.TrimSpace(pr.Host))
	pr.ProviderID = strings.TrimSpace(pr.ProviderID)
	pr.URLAlias = strings.TrimSpace(pr.URLAlias)
	return pr
}

func resolveWeakPRAlias(ctx context.Context, q *gen.Queries, pr domain.PullRequest) (domain.PullRequest, error) {
	if pr.ProviderID != "" || pr.URLAlias != "" {
		return pr, nil
	}
	existing, err := q.GetPRByURLOrAlias(ctx, pr.URL)
	if errors.Is(err, sql.ErrNoRows) {
		return pr, nil
	}
	if err != nil {
		return domain.PullRequest{}, err
	}
	if existing.URL == pr.URL {
		return pr, nil
	}
	pr.URLAlias = pr.URL
	pr.URL = existing.URL
	if pr.Provider == "" {
		pr.Provider = existing.Provider
	}
	if pr.Host == "" {
		pr.Host = existing.Host
	}
	if pr.Repo == "" {
		pr.Repo = existing.Repo
	}
	pr.ProviderID = existing.ProviderID
	return pr, nil
}

func upsertPRWithIdentity(ctx context.Context, q *gen.Queries, pr domain.PullRequest, rejectReassignment bool) error {
	if pr.ProviderID == "" && pr.URLAlias == "" {
		return q.UpsertPR(ctx, genPRParams(pr))
	}
	candidates, err := findPRIdentityCandidates(ctx, q, pr)
	if err != nil {
		return err
	}
	for _, existing := range candidates {
		if rejectReassignment && existing.SessionID != pr.SessionID {
			return fmt.Errorf("pr %s already belongs to session %s", existing.URL, existing.SessionID)
		}
		if existing.URL != pr.URL && existing.ProviderID != "" {
			if err := q.ClearPRProviderIdentity(ctx, existing.URL); err != nil {
				return err
			}
		}
	}
	if err := q.UpsertPR(ctx, genPRParams(pr)); err != nil {
		return err
	}
	if err := q.DeletePRAlias(ctx, pr.URL); err != nil {
		return err
	}
	for previousURL := range candidates {
		if previousURL == pr.URL {
			continue
		}
		if err := movePRAliasRows(ctx, q, previousURL, pr.URL); err != nil {
			return err
		}
		if err := q.RepointPRAliases(ctx, gen.RepointPRAliasesParams{CanonicalURL: pr.URL, PreviousURL: previousURL}); err != nil {
			return err
		}
		if err := q.DeletePRByURL(ctx, previousURL); err != nil {
			return err
		}
		if err := q.UpsertPRAlias(ctx, gen.UpsertPRAliasParams{AliasURL: previousURL, CanonicalURL: pr.URL}); err != nil {
			return err
		}
	}
	if pr.URLAlias != "" && pr.URLAlias != pr.URL {
		if err := q.UpsertPRAlias(ctx, gen.UpsertPRAliasParams{AliasURL: pr.URLAlias, CanonicalURL: pr.URL}); err != nil {
			return err
		}
	}
	return nil
}

func findPRIdentityCandidates(ctx context.Context, q *gen.Queries, pr domain.PullRequest) (map[string]gen.PR, error) {
	candidates := map[string]gen.PR{}
	addCandidate := func(row gen.PR, err error) error {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		candidates[row.URL] = row
		return nil
	}
	if err := addCandidate(q.GetPR(ctx, pr.URL)); err != nil {
		return nil, err
	}
	if pr.URLAlias != "" && pr.URLAlias != pr.URL {
		if err := addCandidate(q.GetPRByURLOrAlias(ctx, pr.URLAlias)); err != nil {
			return nil, err
		}
	}
	if pr.ProviderID != "" && pr.Provider != "" && pr.Host != "" {
		if err := addCandidate(q.GetPRByProviderIdentity(ctx, gen.GetPRByProviderIdentityParams{
			Provider: pr.Provider, Host: pr.Host, ProviderID: pr.ProviderID,
		})); err != nil {
			return nil, err
		}
	}
	return candidates, nil
}

func movePRAliasRows(ctx context.Context, q *gen.Queries, previousURL, canonicalURL string) error {
	if err := q.MovePRAliasChecks(ctx, gen.MovePRAliasChecksParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasChecks(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasReviews(ctx, gen.MovePRAliasReviewsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasReviews(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewThreads(ctx, gen.MovePRAliasReviewThreadsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasReviewThreads(ctx, previousURL); err != nil {
		return err
	}
	if err := q.MovePRAliasComments(ctx, gen.MovePRAliasCommentsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.DeletePRAliasComments(ctx, previousURL); err != nil {
		return err
	}
	if err := q.ResolveConflictingPRAliasNotifications(ctx, gen.ResolveConflictingPRAliasNotificationsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasNotifications(ctx, gen.MovePRAliasNotificationsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewState(ctx, gen.MovePRAliasReviewStateParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	if err := q.MovePRAliasReviewRuns(ctx, gen.MovePRAliasReviewRunsParams{CanonicalURL: canonicalURL, PreviousURL: previousURL}); err != nil {
		return err
	}
	return q.DeletePRAliasReviewRuns(ctx, previousURL)
}

func reviewThreadIDs(threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(threads))
	for _, th := range threads {
		if th.ThreadID == "" || seen[th.ThreadID] {
			continue
		}
		seen[th.ThreadID] = true
		out = append(out, th.ThreadID)
	}
	for _, c := range comments {
		if c.ThreadID == "" || seen[c.ThreadID] {
			continue
		}
		seen[c.ThreadID] = true
		out = append(out, c.ThreadID)
	}
	return out
}
