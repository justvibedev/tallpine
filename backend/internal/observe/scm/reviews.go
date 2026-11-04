package scm

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (o *Observer) refreshReviews(ctx context.Context, subjects map[string]*subject, observations map[string]ports.SCMObservation, subjectsByPR map[string]*subject, reviewModes map[string]ports.ReviewWriteMode, localOnlyObservations, reviewStale map[string]bool, now time.Time) {
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		if s.known.Merged || s.known.Closed {
			continue
		}
		pkey := keyForTrackedPR(s.known, s.repo)
		obs, hasObs := observations[pkey]
		decision := string(s.known.Review)
		if hasObs && obs.Review.Decision != "" {
			decision = obs.Review.Decision
		}
		updatedAtProvider := obs.PR.UpdatedAtProvider
		if !o.needsReviewRefresh(pkey, s.known, decision, hasObs, updatedAtProvider, now) {
			continue
		}
		started := time.Now()
		review, err := o.provider.FetchReviewThreads(ctx, ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL})
		o.logProviderRequest("fetch_review_threads", repoFullName(s.repo), started, err)
		if err != nil {
			o.logger.Error("scm observer: review refresh failed", "pr", s.known.URL, "err", err)
			o.cacheSetBool(o.Cache.ReviewRefreshFailed, &o.Cache.reviewFailedOrder, pkey, true)
			if hasObs {
				obs.Review.Decision = string(s.known.Review)
				obs.Review.Threads = nil
				observations[pkey] = obs
				subjectsByPR[pkey] = s
				reviewStale[pkey] = true
			}
			continue
		}
		if !hasObs {
			checks, err := o.store.ListChecks(ctx, s.known.URL)
			if err != nil {
				o.logger.Error("scm observer: list local checks for review-only refresh failed", "pr", s.known.URL, "err", err)
			}
			obs = observationFromLocal(s.repo, s.known, checks)
			localOnlyObservations[pkey] = true
		}
		// Precedence guard: don't let FetchReviewThreads downgrade a higher-
		// priority decision that was set by the fast-path. Only overwrite when
		// the fetched decision has equal or higher priority. Threads, summaries,
		// and the partial flag are always adopted regardless of the guard.
		if review.Decision != "" && reviewDecisionPriority(domain.ReviewDecision(review.Decision)) >= reviewDecisionPriority(domain.ReviewDecision(obs.Review.Decision)) {
			obs.Review.Decision = review.Decision
		}
		obs.Review.Reviews = review.Reviews
		obs.Review.Threads = review.Threads
		obs.Review.Partial = review.Partial
		obs.ObservedAt = now
		observations[pkey] = obs
		subjectsByPR[pkey] = s
		if review.Partial {
			reviewModes[pkey] = ports.ReviewWriteMerge
		} else {
			reviewModes[pkey] = ports.ReviewWriteReplace
		}
		cacheDelete(o.Cache.ReviewRefreshFailed, &o.Cache.reviewFailedOrder, pkey)
	}
}

// reviewDecisionPriority returns a numeric priority for a review decision.
// Higher values represent more authoritative or blocking decisions. The guard
// in refreshReviews uses this to prevent FetchReviewThreads from downgrading a
// decision that was set by the fast-path (e.g. fetchSingleMR mapping
// detailed_merge_status=requested_changes to ReviewChangesRequest). The guard
// is cross-provider: it also protects GitHub PRs whose CHANGES_REQUESTED status
// was mapped to ReviewChangesRequest in the fast path.
func reviewDecisionPriority(d domain.ReviewDecision) int {
	switch d {
	case domain.ReviewChangesRequest:
		return 3
	case domain.ReviewRequired:
		return 2
	case domain.ReviewApproved:
		return 1
	default:
		return 0
	}
}

func (o *Observer) needsReviewRefresh(key string, local domain.PullRequest, decision string, hasObs bool, updatedAtProvider, now time.Time) bool {
	if o.Cache.ReviewRefreshFailed[key] {
		return true
	}
	if local.ReviewHash == "" {
		return true
	}
	if local.ReviewObservedAt.IsZero() {
		return true
	}
	if decision == string(domain.ReviewChangesRequest) {
		last := o.Cache.LastReviewPollAt[key]
		return last.IsZero() || now.Sub(last) >= o.reviewInterval
	}
	if hasObs && decision != string(local.Review) {
		return true
	}
	if local.ReviewHash != "" && string(local.Review) == string(domain.ReviewChangesRequest) && decision != string(domain.ReviewChangesRequest) {
		return true
	}
	if hasObs && !updatedAtProvider.IsZero() && updatedAtProvider.After(local.ReviewObservedAt) {
		return true
	}
	return false
}
