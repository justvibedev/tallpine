package scm

import (
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

func domainFromObservation(sessionID domain.SessionID, sessionRecord domain.SessionRecord, obs ports.SCMObservation, local domain.PullRequest, opts persistenceOptions, now time.Time) (domain.PullRequest, []domain.PullRequestCheck, []domain.PullRequestReview, []domain.PullRequestReviewThread, []domain.PullRequestComment) {
	metadataHash := metadataSemanticHash(obs)
	if opts.preserveLocalMetadataHash {
		metadataHash = local.MetadataHash
	}
	ciHash := ciSemanticHash(obs.CI)
	if opts.preserveLocalCIHash {
		ciHash = local.CIHash
	}
	reviewHash := reviewSemanticHash(obs.Review)
	reviewDecision := domain.ReviewDecision(textutil.FirstNonBlank(obs.Review.Decision, string(domain.ReviewNone)))
	if opts.preserveLocalReviewDecision {
		reviewDecision = local.Review
	}
	if opts.preserveLocalReviewHash {
		reviewHash = local.ReviewHash
	} else if !opts.reviewFetched && local.ReviewHash != "" && reviewDecision == local.Review {
		reviewHash = local.ReviewHash
	}
	observedAt := obs.ObservedAt
	if !obs.Changed.Metadata && !obs.Changed.CI && !local.ObservedAt.IsZero() {
		observedAt = local.ObservedAt
	}
	ciObservedAt := local.CIObservedAt
	if obs.Changed.CI || ciObservedAt.IsZero() {
		ciObservedAt = obs.ObservedAt
	}
	// Only a successful review-thread fetch establishes a review observation:
	// a metadata/CI-only pass (or a failed review fetch in preserve mode) must
	// not manufacture one, or a never-fetched review storage would look
	// complete to the summary gate and publish a known-looking zero.
	reviewObservedAt := local.ReviewObservedAt
	if opts.reviewFetched {
		reviewObservedAt = obs.ObservedAt
	}
	// Partial-ness follows the last fetched review observation; when this pass
	// did not fetch reviews, keep the local record so the summary layer can
	// keep treating stored thread rows as a partial view.
	reviewPartial := local.ReviewPartial
	if opts.reviewFetched {
		reviewPartial = obs.Review.Partial
	}
	pr := domain.PullRequest{
		URL:                      textutil.FirstNonBlank(obs.PR.URL, obs.PR.HTMLURL),
		URLAlias:                 obs.PR.URLAlias,
		SessionID:                sessionID,
		Number:                   obs.PR.Number,
		Draft:                    obs.PR.Draft,
		Merged:                   obs.PR.Merged,
		Closed:                   obs.PR.Closed,
		CI:                       domain.CIState(textutil.FirstNonBlank(obs.CI.Summary, string(domain.CIUnknown))),
		Review:                   reviewDecision,
		Mergeability:             domain.Mergeability(textutil.FirstNonBlank(obs.Mergeability.State, string(domain.MergeUnknown))),
		UpdatedAt:                now,
		Provider:                 obs.Provider,
		Host:                     obs.Host,
		Repo:                     obs.Repo,
		ProviderID:               obs.PR.ProviderID,
		SourceBranch:             obs.PR.SourceBranch,
		TargetBranch:             obs.PR.TargetBranch,
		HeadSHA:                  obs.PR.HeadSHA,
		Title:                    obs.PR.Title,
		Additions:                obs.PR.Additions,
		Deletions:                obs.PR.Deletions,
		ChangedFiles:             obs.PR.ChangedFiles,
		Paths:                    obs.PR.Paths,
		Author:                   obs.PR.Author,
		AuthorAvatarURL:          obs.PR.AuthorAvatarURL,
		BaseSHA:                  obs.PR.BaseSHA,
		MergeCommitSHA:           obs.PR.MergeCommitSHA,
		ProviderState:            obs.PR.ProviderState,
		ProviderMergeable:        obs.PR.ProviderMergeable,
		ProviderMergeStateStatus: obs.PR.ProviderMergeStateStatus,
		HTMLURL:                  obs.PR.HTMLURL,
		CreatedAtProvider:        obs.PR.CreatedAtProvider,
		UpdatedAtProvider:        obs.PR.UpdatedAtProvider,
		MergedAtProvider:         obs.PR.MergedAtProvider,
		ClosedAtProvider:         obs.PR.ClosedAtProvider,
		MetadataHash:             metadataHash,
		CIHash:                   ciHash,
		ReviewHash:               reviewHash,
		ObservedAt:               observedAt,
		CIObservedAt:             ciObservedAt,
		ReviewObservedAt:         reviewObservedAt,
		ReviewPartial:            reviewPartial,
	}
	checks := make([]domain.PullRequestCheck, 0, len(obs.CI.Checks))
	for _, ch := range obs.CI.Checks {
		checks = append(checks, domain.PullRequestCheck{Name: ch.Name, CommitHash: obs.CI.HeadSHA, Status: domain.PRCheckStatus(ch.Status), Conclusion: ch.Conclusion, URL: ch.URL, Details: ch.ProviderID, LogTail: ch.LogTail, CreatedAt: now})
	}
	reviews := make([]domain.PullRequestReview, 0, len(obs.Review.Reviews))
	for _, review := range obs.Review.Reviews {
		reviews = append(reviews, domain.PullRequestReview{
			ID:               review.ID,
			Author:           review.Author,
			State:            domain.ReviewDecision(textutil.FirstNonBlank(review.State, string(domain.ReviewNone))),
			URL:              review.URL,
			Body:             review.Body,
			IsBot:            review.IsBot,
			TargetSHA:        review.TargetSHA,
			SubmittedAt:      firstTime(review.SubmittedAt, now),
			AutoInjectReview: sessionRecord.AutoInjectReview,
		})
	}
	threads := make([]domain.PullRequestReviewThread, 0, len(obs.Review.Threads))
	commentCount := 0
	for _, th := range obs.Review.Threads {
		commentCount += len(th.Comments)
	}
	comments := make([]domain.PullRequestComment, 0, commentCount)
	for _, th := range obs.Review.Threads {
		threads = append(threads, domain.PullRequestReviewThread{ThreadID: th.ID, Path: th.Path, Line: th.Line, Resolved: th.Resolved, IsBot: th.IsBot, SemanticHash: threadSemanticHash(th), UpdatedAt: now})
		for _, c := range th.Comments {
			comments = append(comments, domain.PullRequestComment{ThreadID: th.ID, ReviewID: c.ReviewID, ID: c.ID, Author: c.Author, File: th.Path, Line: th.Line, Body: c.Body, URL: c.URL, Resolved: th.Resolved, IsBot: c.IsBot, CreatedAt: now, AutoInjectReview: sessionRecord.AutoInjectReview})
		}
	}
	return pr, checks, reviews, threads, comments
}

func observationFromLocal(repo ports.SCMRepo, pr domain.PullRequest, checks []domain.PullRequestCheck) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:      true,
		Provider:     textutil.FirstNonBlank(pr.Provider, repo.Provider),
		Host:         textutil.FirstNonBlank(pr.Host, repo.Host),
		Repo:         textutil.FirstNonBlank(pr.Repo, repoFullName(repo)),
		PR:           ports.SCMPRObservation{URL: pr.URL, Number: pr.Number, State: string(domain.PRLifecycleState(pr.Merged, pr.Closed, pr.Draft)), Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch, HeadSHA: pr.HeadSHA, Title: pr.Title, Additions: pr.Additions, Deletions: pr.Deletions, ChangedFiles: pr.ChangedFiles, Paths: pr.Paths, Author: pr.Author, AuthorAvatarURL: pr.AuthorAvatarURL, BaseSHA: pr.BaseSHA, MergeCommitSHA: pr.MergeCommitSHA, ProviderState: pr.ProviderState, ProviderMergeable: pr.ProviderMergeable, ProviderMergeStateStatus: pr.ProviderMergeStateStatus, HTMLURL: pr.HTMLURL, CreatedAtProvider: pr.CreatedAtProvider, UpdatedAtProvider: pr.UpdatedAtProvider, MergedAtProvider: pr.MergedAtProvider, ClosedAtProvider: pr.ClosedAtProvider},
		CI:           ciObservationFromLocal(pr, checks),
		Review:       ports.SCMReviewObservation{Decision: string(pr.Review)},
		Mergeability: mergeabilityObservationFromLocal(pr),
	}
}

func ciObservationFromLocal(pr domain.PullRequest, checks []domain.PullRequestCheck) ports.SCMCIObservation {
	ci := ports.SCMCIObservation{
		Summary:           textutil.FirstNonBlank(string(pr.CI), string(domain.CIUnknown)),
		HeadSHA:           pr.HeadSHA,
		FailedFingerprint: failedFingerprintFromCIHash(pr.CIHash),
	}
	tails := []string{}
	for _, ch := range checks {
		if pr.HeadSHA != "" && ch.CommitHash != "" && ch.CommitHash != pr.HeadSHA {
			continue
		}
		if ci.HeadSHA == "" {
			ci.HeadSHA = ch.CommitHash
		}
		check := ports.SCMCheckObservation{
			Name:       ch.Name,
			Status:     string(ch.Status),
			Conclusion: ch.Conclusion,
			URL:        ch.URL,
			LogTail:    ch.LogTail,
			ProviderID: ch.Details,
		}
		ci.Checks = append(ci.Checks, check)
		if ch.Status == domain.PRCheckFailed || ch.Status == domain.PRCheckCancelled {
			ci.FailedChecks = append(ci.FailedChecks, check)
			if ch.LogTail != "" {
				tails = append(tails, ch.LogTail)
			}
		}
	}
	ci.FailureLogTail = strings.Join(tails, "\n---\n")
	return ci
}

func failedFingerprintFromCIHash(hash string) string {
	before, _, ok := strings.Cut(hash, ":")
	if !ok {
		return ""
	}
	return before
}

func mergeabilityObservationFromLocal(pr domain.PullRequest) ports.SCMMergeabilityObservation {
	out := mergeabilityFromProviderFacts(pr.ProviderMergeable, pr.ProviderMergeStateStatus, string(pr.CI), string(pr.Review), pr.Draft)
	if pr.Mergeability != "" && out.State != string(pr.Mergeability) {
		out = ports.SCMMergeabilityObservation{State: string(pr.Mergeability)}
	} else if pr.Mergeability != "" {
		out.State = string(pr.Mergeability)
	}
	switch domain.Mergeability(out.State) {
	case domain.MergeMergeable:
		out.Mergeable = true
	case domain.MergeConflicting:
		out.Conflict = true
		if len(out.Blockers) == 0 {
			out.Blockers = append(out.Blockers, "conflicts")
		}
	case domain.MergeBlocked:
		if len(out.Blockers) == 0 {
			out.Blockers = mergeBlockersFromLocal(pr)
		}
	}
	return out
}

func mergeBlockersFromLocal(pr domain.PullRequest) []string {
	blockers := []string{}
	if pr.Draft {
		blockers = append(blockers, "draft")
	}
	if pr.CI == domain.CIFailing {
		blockers = append(blockers, "ci_failing")
	}
	switch pr.Review {
	case domain.ReviewChangesRequest:
		blockers = append(blockers, "changes_requested")
	case domain.ReviewRequired:
		blockers = append(blockers, "review_required")
	}
	if len(blockers) == 0 {
		blockers = append(blockers, "blocked_by_provider")
	}
	return blockers
}

func mergeabilityFromProviderFacts(providerMergeable, providerMergeState, ci, review string, draft bool) ports.SCMMergeabilityObservation {
	state := strings.ToUpper(strings.TrimSpace(providerMergeState))
	mergeable := strings.ToUpper(strings.TrimSpace(providerMergeable))
	out := ports.SCMMergeabilityObservation{State: string(domain.MergeUnknown)}
	addBlocker := func(b string) { out.Blockers = append(out.Blockers, b) }
	if state == "DIRTY" || mergeable == "CONFLICTING" {
		out.State = string(domain.MergeConflicting)
		out.Conflict = true
		addBlocker("conflicts")
		return out
	}
	if state == "BEHIND" || state == "BEHIND_BASE" {
		out.BehindBase = true
		addBlocker("behind_base")
	}
	if state == "BLOCKED" {
		out.State = string(domain.MergeBlocked)
		addBlocker("blocked_by_provider")
	}
	// UNSTABLE outranks draft / CI / review (doc.go rule 3, and the
	// mergeabilityFromGraphQL sibling): GitHub reports UNSTABLE when the PR
	// *is* mergeable but a non-required check failed or is pending. Such a PR
	// also has a FAILURE rollup (-> ci == failing), so leaving this below the
	// blockers downgraded every genuinely-mergeable UNSTABLE PR to blocked.
	if state == "UNSTABLE" {
		out.State = string(domain.MergeUnstable)
		return out
	}
	if draft {
		out.State = string(domain.MergeBlocked)
		addBlocker("draft")
	}
	if ci == string(domain.CIFailing) {
		out.State = string(domain.MergeBlocked)
		addBlocker("ci_failing")
	}
	switch review {
	case string(domain.ReviewChangesRequest):
		out.State = string(domain.MergeBlocked)
		addBlocker("changes_requested")
	case string(domain.ReviewRequired):
		out.State = string(domain.MergeBlocked)
		addBlocker("review_required")
	}
	if out.State == string(domain.MergeBlocked) {
		return out
	}
	if mergeable == "MERGEABLE" && (state == "CLEAN" || state == "HAS_HOOKS" || state == "") &&
		(review == "" || review == string(domain.ReviewApproved) || review == string(domain.ReviewNone)) && !draft {
		out.State = string(domain.MergeMergeable)
		out.Mergeable = true
		return out
	}
	return out
}

func chunks[T any](in []T, n int) [][]T {
	if n <= 0 || len(in) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(in)+n-1)/n)
	for len(in) > 0 {
		end := n
		if len(in) < end {
			end = len(in)
		}
		out = append(out, in[:end])
		in = in[end:]
	}
	return out
}
