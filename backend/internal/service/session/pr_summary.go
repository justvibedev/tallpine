package session

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/pkg/contract"
)

// PRSummary is the user-facing SCM read model for one PR owned by a session.
type PRSummary = contract.PullRequestSummary

// PRCISummary describes the latest CI status and failing checks for a PR.
type PRCISummary = contract.PullRequestCISummary

// PRFailingCheck is one failed or cancelled CI check for a PR.
type PRFailingCheck = contract.PullRequestFailingCheck

// PRReviewSummary describes the latest review decision and unresolved comments.
type PRReviewSummary = contract.PullRequestReviewSummary

// PRReviewEntry is one submitted provider review summary: a reviewer's decisive
// verdict and the body they submitted with it.
type PRReviewEntry = contract.PullRequestSubmittedReview

// PRUnresolvedReviewer groups unresolved human comments by reviewer.
type PRUnresolvedReviewer = contract.PullRequestUnresolvedReviewer

// PRReviewCommentLink points to one unresolved review comment.
type PRReviewCommentLink = contract.PullRequestReviewCommentLink

// PRMergeabilitySummary describes whether a PR can be merged and why.
type PRMergeabilitySummary = contract.PullRequestMergeabilitySummary

// PRConflictFile is one file involved in a PR merge conflict.
type PRConflictFile = contract.PullRequestConflictFile

// ListPRSummaries returns all PRs owned by a session with concise SCM details
// assembled from persisted PR/check/review facts.
func (s *Service) ListPRSummaries(ctx context.Context, id domain.SessionID) ([]PRSummary, error) {
	if _, ok, err := s.store.GetSession(ctx, id); err != nil {
		return nil, fmt.Errorf("get %s: %w", id, err)
	} else if !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, err
	}
	groups := groupPullRequestAliases(prs)
	runs, err := s.store.ListCurrentHeadReviewRunsForSession(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]PRSummary, 0, len(groups))
	for _, group := range groups {
		var checks []domain.PullRequestCheck
		var threads []domain.PullRequestReviewThread
		var reviews []domain.PullRequestReview
		var comments []domain.PullRequestComment
		threadsExact := true
		for _, pr := range group.aliases {
			prChecks, err := s.store.ListChecks(ctx, pr.URL)
			if err != nil {
				return nil, err
			}
			checks = append(checks, prChecks...)
			prThreads, err := s.store.ListPRReviewThreads(ctx, pr.URL)
			if err != nil {
				return nil, err
			}
			threads = append(threads, prThreads...)
			prReviews, err := s.store.ListPRReviews(ctx, pr.URL)
			if err != nil {
				return nil, err
			}
			reviews = append(reviews, prReviews...)
			prComments, err := s.store.ListPRComments(ctx, pr.URL)
			if err != nil {
				return nil, err
			}
			comments = append(comments, prComments...)
			// The thread count is only exact when the stored rows are a complete
			// observation: never-fetched reviews leave no rows at all, and a
			// partial fetch (provider thread-window cap) both misses rows outside
			// the window and preserves stale unresolved rows via the merge write.
			if pr.ReviewPartial || pr.ReviewObservedAt.IsZero() {
				threadsExact = false
			}
		}
		summary := summarizePR(group.primary, checks, reviews, threads, comments, threadsExact)
		urls := make([]string, 0, len(group.aliases))
		for _, pr := range group.aliases {
			urls = append(urls, pr.URL)
		}
		summary.Review.Decision = decisionWithTallpineVerdict(summary.Review.Decision, urls, runs)
		out = append(out, summary)
	}
	sortPRSummaries(out)
	return out, nil
}

// PRListing keeps reported references separate from SCM facts and actions.
type PRListing struct {
	Tracked []PRSummary
	Linked  []domain.ChangeRequestReference
}

// ListPRListing combines tracked PRs with created PRs reported by the worker.
func (s *Service) ListPRListing(ctx context.Context, id domain.SessionID) (PRListing, error) {
	tracked, err := s.ListPRSummaries(ctx, id)
	if err != nil {
		return PRListing{}, err
	}
	urls, err := s.store.ListReportedPRURLs(ctx, id)
	if err != nil {
		return PRListing{}, err
	}
	seen := make(map[string]bool, len(tracked)+len(urls))
	for _, pr := range tracked {
		ref, err := domain.ParseChangeRequestURL(pr.HTMLURL)
		if err != nil {
			ref, err = domain.ParseChangeRequestURL(pr.URL)
		}
		if err == nil {
			seen[ref.Key()] = true
		}
	}
	linked := make([]domain.ChangeRequestReference, 0, len(urls))
	for _, raw := range urls {
		ref, err := domain.ParseChangeRequestURL(raw)
		if err != nil || seen[ref.Key()] {
			continue
		}
		seen[ref.Key()] = true
		linked = append(linked, ref)
	}
	return PRListing{Tracked: tracked, Linked: linked}, nil
}

func summarizePR(pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, threadsExact bool) PRSummary {
	return PRSummary{
		URL:              pr.URL,
		HTMLURL:          cmp.Or(pr.HTMLURL, pr.URL),
		Number:           pr.Number,
		Title:            pr.Title,
		State:            pr.State(),
		Provider:         cmp.Or(pr.Provider, "github"),
		Repo:             pr.Repo,
		Author:           pr.Author,
		AuthorAvatarURL:  pr.AuthorAvatarURL,
		SourceBranch:     pr.SourceBranch,
		TargetBranch:     pr.TargetBranch,
		HeadSHA:          pr.HeadSHA,
		Additions:        pr.Additions,
		Deletions:        pr.Deletions,
		ChangedFiles:     pr.ChangedFiles,
		CI:               summarizeCI(pr, checks),
		Review:           summarizeReview(pr, comments, reviews, threads, threadsExact),
		Mergeability:     summarizeMergeability(pr, threads),
		StateChangedAt:   summarizePRStateChangedAt(pr),
		CreatedAt:        pr.CreatedAtProvider,
		UpdatedAt:        pr.UpdatedAt,
		ObservedAt:       pr.ObservedAt,
		CIObservedAt:     pr.CIObservedAt,
		ReviewObservedAt: pr.ReviewObservedAt,
	}
}

func summarizePRStateChangedAt(pr domain.PullRequest) time.Time {
	if !pr.StateChangedAt.IsZero() {
		return pr.StateChangedAt
	}
	switch pr.State() {
	case domain.PRStateMerged:
		return pr.MergedAtProvider
	case domain.PRStateClosed:
		return pr.ClosedAtProvider
	case domain.PRStateDraft, domain.PRStateOpen:
		return pr.CreatedAtProvider
	default:
		return time.Time{}
	}
}

func summarizeCI(pr domain.PullRequest, checks []domain.PullRequestCheck) PRCISummary {
	state := ciOrUnknown(pr.CI)
	out := PRCISummary{State: state, AutoInjectCI: pr.AutoInjectCI}
	if state != domain.CIFailing || pr.Merged || pr.Closed {
		return out
	}
	for _, ch := range checks {
		if ch.Status != domain.PRCheckFailed && ch.Status != domain.PRCheckCancelled {
			continue
		}
		if pr.HeadSHA != "" && ch.CommitHash != "" && !strings.EqualFold(ch.CommitHash, pr.HeadSHA) {
			continue
		}
		out.FailingChecks = append(out.FailingChecks, PRFailingCheck{
			Name:       ch.Name,
			Status:     ch.Status,
			Conclusion: ch.Conclusion,
			URL:        ch.URL,
		})
	}
	return out
}

func summarizeReview(pr domain.PullRequest, comments []domain.PullRequestComment, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, threadsExact bool) PRReviewSummary {
	// A nil count means "unknown", not zero: it must stay absent on the wire
	// rather than present-as-zero. It is only published when the stored thread
	// rows are a complete observation.
	var unresolvedThreadCount *int
	if threadsExact {
		count := unresolvedHumanThreadCount(dedupeReviewThreads(threads))
		unresolvedThreadCount = &count
	}
	out := PRReviewSummary{Decision: reviewOrNone(pr.Review), UnresolvedThreadCount: unresolvedThreadCount}
	if pr.Merged || pr.Closed {
		return out
	}
	byReviewer := map[string]int{}
	resolvedByReviewer := map[string]int{}
	order := []string{}
	resolvedOrder := []string{}
	links := map[string][]PRReviewCommentLink{}
	resolvedLinks := map[string][]PRReviewCommentLink{}
	isBot := map[string]bool{}
	resolvedIsBot := map[string]bool{}
	for _, c := range comments {
		if c.IsBot {
			continue
		}
		reviewer := strings.TrimSpace(c.Author)
		if reviewer == "" {
			reviewer = "unknown"
		}
		link := PRReviewCommentLink{
			URL:              c.URL,
			ReviewID:         c.ReviewID,
			File:             c.File,
			Line:             c.Line,
			Body:             c.Body,
			AutoInjectReview: c.AutoInjectReview,
		}
		if c.Resolved {
			if _, ok := resolvedByReviewer[reviewer]; !ok {
				resolvedOrder = append(resolvedOrder, reviewer)
			}
			resolvedByReviewer[reviewer]++
			resolvedIsBot[reviewer] = c.IsBot
			resolvedLinks[reviewer] = append(resolvedLinks[reviewer], link)
			continue
		}
		if _, ok := byReviewer[reviewer]; !ok {
			order = append(order, reviewer)
		}
		byReviewer[reviewer]++
		isBot[reviewer] = c.IsBot
		links[reviewer] = append(links[reviewer], link)
	}
	latestReviews := latestChangesRequestedReviews(reviews)
	reviewURLByAuthor := map[string]string{}
	for reviewer, review := range latestReviews {
		if _, ok := byReviewer[reviewer]; !ok {
			order = append(order, reviewer)
		}
		reviewURLByAuthor[reviewer] = review.URL
		isBot[reviewer] = review.IsBot
	}
	// Reviews carries every reviewer's latest submitted review, including
	// non-decisive COMMENTED reviews normalized as ReviewNone. The unresolved
	// grouping above still uses decisive changes-requested reviews only.
	for reviewer, review := range latestReviewSummaries(reviews) {
		out.Reviews = append(out.Reviews, PRReviewEntry{
			Reviewer:         reviewer,
			Verdict:          reviewOrNone(review.State),
			Body:             review.Body,
			URL:              review.URL,
			SubmittedAt:      review.SubmittedAt,
			IsBot:            review.IsBot,
			AutoInjectReview: review.AutoInjectReview,
		})
	}
	sort.Slice(out.Reviews, func(i, j int) bool { return out.Reviews[i].Reviewer < out.Reviews[j].Reviewer })
	sort.Strings(order)
	for _, reviewer := range order {
		out.UnresolvedBy = append(out.UnresolvedBy, PRUnresolvedReviewer{
			ReviewerID: reviewer,
			Count:      byReviewer[reviewer],
			Links:      links[reviewer],
			ReviewURL:  reviewURLByAuthor[reviewer],
			IsBot:      isBot[reviewer],
		})
	}
	sort.Strings(resolvedOrder)
	for _, reviewer := range resolvedOrder {
		out.ResolvedBy = append(out.ResolvedBy, PRUnresolvedReviewer{
			ReviewerID: reviewer,
			Count:      resolvedByReviewer[reviewer],
			Links:      resolvedLinks[reviewer],
			ReviewURL:  reviewURLByAuthor[reviewer],
			IsBot:      resolvedIsBot[reviewer],
		})
	}
	for _, reviewer := range out.UnresolvedBy {
		if reviewer.Count > 0 && !reviewer.IsBot {
			out.HasUnresolvedHumanComments = true
			break
		}
	}
	return out
}

func summarizeMergeability(pr domain.PullRequest, _ []domain.PullRequestReviewThread) PRMergeabilitySummary {
	return PRMergeabilitySummary{
		State:   mergeabilityOrUnknown(pr.Mergeability),
		Reasons: mergeabilityReasons(pr),
		PRURL:   cmp.Or(pr.HTMLURL, pr.URL),
	}
}

func mergeabilityReasons(pr domain.PullRequest) []string {
	if pr.Merged || pr.Closed {
		return nil
	}
	if pr.Mergeability != domain.MergeConflicting && pr.Mergeability != domain.MergeBlocked && pr.Mergeability != domain.MergeUnstable {
		return nil
	}
	reasons := map[string]bool{}
	add := func(reason string) {
		if reason != "" {
			reasons[reason] = true
		}
	}
	if pr.Mergeability == domain.MergeConflicting || containsAny(pr.ProviderMergeable, "conflict", "dirty") || containsAny(pr.ProviderMergeStateStatus, "conflict", "dirty") {
		add("conflicts")
	}
	if containsAny(pr.ProviderMergeStateStatus, "behind") {
		add("behind_base")
	}
	if pr.Draft {
		add("draft")
	}
	if pr.CI == domain.CIFailing {
		add("ci_failing")
	}
	if pr.Review == domain.ReviewChangesRequest {
		add("changes_requested")
	}
	if pr.Review == domain.ReviewRequired {
		add("review_required")
	}
	if pr.NoCIAwaitingApproval() {
		add(domain.MergeReasonTallpineReviewRequired)
	}
	if pr.Mergeability == domain.MergeBlocked && len(reasons) == 0 {
		add("blocked_by_provider")
	}
	if pr.Mergeability == domain.MergeUnstable && len(reasons) == 0 {
		add("blocked_by_provider")
	}
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

func containsAny(s string, needles ...string) bool {
	s = strings.ToLower(s)
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

func sortPRSummaries(prs []PRSummary) {
	sort.SliceStable(prs, func(i, j int) bool {
		ia, ja := prSummaryActive(prs[i]), prSummaryActive(prs[j])
		if ia != ja {
			return ia
		}
		return prs[i].UpdatedAt.After(prs[j].UpdatedAt)
	})
}

func prSummaryActive(pr PRSummary) bool {
	return pr.State != domain.PRStateMerged && pr.State != domain.PRStateClosed
}

func ciOrUnknown(state domain.CIState) domain.CIState {
	if state == "" {
		return domain.CIUnknown
	}
	return state
}

// decisionWithTallpineVerdict lets the latest Tallpine review verdict on the PR's current
// head stand in for a provider decision that says nothing. Tallpine reviews post as
// comments or as the PR author's own account, so the provider often reports
// "none" for a head Tallpine has in fact approved or asked changes on.
func decisionWithTallpineVerdict(decision domain.ReviewDecision, prURLs []string, runs []domain.CurrentHeadReviewRun) domain.ReviewDecision {
	if decision != domain.ReviewNone && decision != "" {
		return decision
	}
	var latest *domain.CurrentHeadReviewRun
	for i := range runs {
		run := runs[i]
		if !run.Verdict.Valid() || !slices.Contains(prURLs, run.PRURL) {
			continue
		}
		if latest == nil || currentHeadRunOutranks(run, *latest) {
			latest = &run
		}
	}
	switch {
	case latest == nil:
		return decision
	case latest.Verdict == domain.VerdictChangesRequested:
		return domain.ReviewChangesRequest
	default:
		return domain.ReviewApproved
	}
}

func reviewOrNone(decision domain.ReviewDecision) domain.ReviewDecision {
	if decision == "" {
		return domain.ReviewNone
	}
	return decision
}

func mergeabilityOrUnknown(state domain.Mergeability) domain.Mergeability {
	if state == "" {
		return domain.MergeUnknown
	}
	return state
}
