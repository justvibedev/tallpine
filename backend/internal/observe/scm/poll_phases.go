package scm

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// fetchObservationBatches fetches the selected PR refs in provider batches into observations.
func (o *Observer) fetchObservationBatches(ctx context.Context, selection refreshSelection, now time.Time, observations map[string]ports.SCMObservation, markRepoRefreshFailed func(ports.SCMRepo)) error {
	for start := 0; start < len(selection.refs); start += BatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := min(start+BatchSize, len(selection.refs))
		// Filter out cooled-down providers so a rate-limited GitLab does not
		// suppress healthy GitHub refs in the same chunk. activeKeys stays
		// positionally aligned with active: FetchPullRequests answers
		// result[i] for active[i], so activeKeys[i] is the subject key each
		// observation is attributed to — no content-based matching, which a
		// repo rename would make ambiguous.
		active := make([]ports.SCMPRRef, 0, end-start)
		activeKeys := make([]string, 0, end-start)
		for i := start; i < end; i++ {
			ref := selection.refs[i]
			if o.inRateLimitCooldown(now, ref.Repo.Provider) {
				// Item 3 — cooldown-skip marks refresh-incomplete: when a ref
				// is skipped under cooldown, its repository is marked as
				// refresh-incomplete so the repo ETag and LastSyncCursor do
				// not advance without an observation being fetched. Without
				// this, a subsequent 304 can make the skipped update
				// unrecoverable (cross-cutting durable-state preservation
				// rule, review finding #1).
				markRepoRefreshFailed(ref.Repo)
				continue
			}
			active = append(active, ref)
			activeKeys = append(activeKeys, selection.refKeys[i])
		}
		if len(active) == 0 {
			continue
		}
		started := time.Now()
		batch, err := o.provider.FetchPullRequests(ctx, active)
		o.logProviderRequest("fetch_pull_requests", repoNames(active), started, err)
		if err != nil {
			// If the failure is a rate-limit error, apply a per-provider
			// cooldown so subsequent polls back off instead of hammering
			// every 30s
			if cooldown, ok := rateLimitCooldown(now, err); ok {
				for _, ref := range active {
					o.setRateLimitCooldown(now, ref.Repo.Provider, cooldown)
				}
				o.logger.Warn("scm observer: provider rate-limited; entering cooldown", "cooldown", cooldown, "err", err)
				for _, ref := range active {
					markRepoRefreshFailed(ref.Repo)
				}
				continue
			}
			o.logger.Error("scm observer: GraphQL PR batch failed", "err", err)
			for _, ref := range active {
				markRepoRefreshFailed(ref.Repo)
			}
			continue
		}
		for i, ref := range active {
			// Reject Fetched=false observations (missing PR, or a transient
			// failure attached by the multi dispatcher as per-observation
			// Error metadata) so they do not overwrite durable facts, and
			// mark the ref's repo refresh-incomplete either way:
			//   - rate-limit error → per-provider cooldown;
			//   - other/no error → the repo ETag/cursor simply must not
			//     advance without an observation (review finding #1).
			if i >= len(batch) || !batch[i].Fetched {
				if i < len(batch) && batch[i].Error != nil {
					fetchErr := batch[i].Error
					providerKey := textutil.FirstNonBlank(batch[i].Provider, ref.Repo.Provider)
					if errors.Is(fetchErr, ports.ErrSCMNotFound) {
						// A permanent miss (deleted repo, revoked access, dead
						// redirect), re-observed every tick — Debug, not Warn,
						// so a broken tracked PR does not flood the log.
						o.logger.Debug("scm observer: tracked PR not found at provider; marking refresh-incomplete", "provider", providerKey, "pr", ref.URL, "err", fetchErr)
					} else if cooldown, ok := rateLimitCooldown(now, fetchErr); ok {
						o.setRateLimitCooldown(now, providerKey, cooldown)
						o.logger.Warn("scm observer: provider rate-limited (per-observation); entering cooldown", "provider", providerKey, "cooldown", cooldown, "err", fetchErr)
					} else {
						o.logger.Warn("scm observer: provider fetch failed (per-observation); marking refresh-incomplete", "provider", providerKey, "err", fetchErr)
					}
				}
				markRepoRefreshFailed(ref.Repo)
				continue
			}
			obs := batch[i]
			obs.ObservedAt = now
			observations[activeKeys[i]] = obs
		}
	}
	return nil
}

// applyNoCIPolicies resolves the project's no-CI opt-in for each observation.
func (o *Observer) applyNoCIPolicies(ctx context.Context, selection refreshSelection, observations map[string]ports.SCMObservation) error {
	noCI := o.projectNoCILookup()
	for key, subj := range selection.subjectsByPR {
		if err := ctx.Err(); err != nil {
			return err
		}
		obs, ok := observations[key]
		if !ok {
			continue
		}
		local := subj.known
		o.enrichFailureLogs(ctx, &obs, local)
		// Resolve the project's no-CI opt-in and Tallpine review gate before anything
		// is persisted or handed to lifecycle, so every readiness consumer sees
		// one CI state.
		if ports.NoCIGateApplies(obs) && noCI(ctx, subj.session.ProjectID) {
			obs = ports.ApplyNoCIPolicy(obs, true, o.noCIReviewGate(ctx, subj.session.ID, textutil.FirstNonBlank(subj.known.URL, obs.PR.URL, obs.PR.HTMLURL), obs.PR.HeadSHA))
		}
		observations[key] = obs
	}
	return nil
}

// persistObservations writes each observation, notifies lifecycle and records
// which PRs refreshed completely in prRefreshOK.
func (o *Observer) persistObservations(ctx context.Context, selection refreshSelection, observations map[string]ports.SCMObservation, reviewModes map[string]ports.ReviewWriteMode, localOnlyObservations, reviewStale, prRefreshOK map[string]bool, now time.Time, markRepoRefreshFailed, markRepoRefreshOK func(ports.SCMRepo)) error {
	for _, key := range dispatchOrder(observations, selection.subjectsByPR) {
		if err := ctx.Err(); err != nil {
			return err
		}
		obs := observations[key]
		// Nil out transient per-observation failure metadata before persistence.
		// obs.Error is routing-only metadata (review Item 7): it carries a
		// provider failure for cooldown/refresh-incomplete routing above, and
		// must never reach the storage layer or lifecycle so durable state is
		// not corrupted by transient failures (cross-cutting rule, finding 1).
		obs.Error = nil
		observations[key] = obs
		subj, ok := selection.subjectsByPR[key]
		if !ok {
			continue
		}
		local := subj.known
		reviewMode := reviewModes[key]
		opts := persistenceOptions{
			reviewFetched:               reviewMode != ports.ReviewWritePreserve,
			preserveLocalMetadataHash:   localOnlyObservations[key],
			preserveLocalCIHash:         localOnlyObservations[key] || obs.CI.Partial,
			preserveLocalReviewHash:     reviewStale[key],
			preserveLocalReviewDecision: reviewStale[key],
		}
		prepared := o.prepareForPersistence(obs, local, opts, now)
		if !prepared.Changed.Metadata && !prepared.Changed.CI && !prepared.Changed.Review {
			prRefreshOK[key] = true
			continue
		}
		finalPR, finalChecks, finalReviews, finalThreads, finalComments := domainFromObservation(subj.session.ID, subj.session, prepared, local, opts, now)
		pr, checks, reviews, threads, comments := finalPR, finalChecks, finalReviews, finalThreads, finalComments
		// Lifecycle is allowed to run only after the observed facts are durable,
		// but semantic hashes are the observer's acknowledgement cursor. Keep
		// changed hashes at their local values until lifecycle succeeds; if the
		// daemon restarts after a lifecycle failure, the stale hashes force the
		// same observation to be fetched and delivered again.
		if o.lifecycle != nil {
			pendingOpts := opts
			if prepared.Changed.Metadata {
				pendingOpts.preserveLocalMetadataHash = true
			}
			if prepared.Changed.CI {
				pendingOpts.preserveLocalCIHash = true
			}
			if prepared.Changed.Review {
				pendingOpts.preserveLocalReviewHash = true
			}
			pr, checks, reviews, threads, comments = domainFromObservation(subj.session.ID, subj.session, prepared, local, pendingOpts, now)
		}
		if err := o.store.WriteSCMObservation(ctx, pr, checks, reviews, threads, comments, reviewMode); err != nil {
			o.logger.Error("scm observer: DB write failed", "session", subj.session.ID, "pr", pr.URL, "err", err)
			markRepoRefreshFailed(subj.repo)
			continue
		}
		if o.lifecycle != nil {
			if err := o.lifecycle.ApplySCMObservation(ctx, subj.session.ID, prepared); err != nil {
				o.logger.Error("scm observer: lifecycle notification failed", "session", subj.session.ID, "pr", textutil.FirstNonBlank(prepared.PR.URL, prepared.PR.HTMLURL, local.URL), "err", err)
				markRepoRefreshFailed(subj.repo)
				continue
			}
			if err := o.store.WriteSCMObservation(ctx, finalPR, finalChecks, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				o.logger.Error("scm observer: DB lifecycle acknowledgement failed", "session", subj.session.ID, "pr", finalPR.URL, "err", err)
				markRepoRefreshFailed(subj.repo)
				continue
			}
		}
		// If this observation came from terminal reconciliation, a successful
		// terminal persistence (merged/closed transition observed and
		// persisted) un-marks the repo refresh-incomplete so the repo
		// ETag/cursor may advance. A "still open" no-op result never reaches
		// this branch (it returns at the unchanged-hashes check above), so
		// its repo stays refresh-incomplete and the ETag/cursor stay pinned.
		markRepoRefreshOK(subj.repo)
		prRefreshOK[key] = true
	}
	return nil
}

// commitPollProgress advances per-PR caches and the repo ETag/cursor, but only
// for refs and repos that refreshed completely.
func (o *Observer) commitPollProgress(selection refreshSelection, prRefreshOK map[string]bool, reviewModes map[string]ports.ReviewWriteMode, repoRefreshOK map[string]bool, repoGuards map[string]repoGuardState, listedRepos map[string]bool, now time.Time) {
	for key, ok := range prRefreshOK {
		if !ok {
			continue
		}
		if pending, found := selection.commitETags[key]; found {
			o.cacheSetString(o.Cache.CommitChecksETag, &o.Cache.commitOrder, pending.key, pending.value)
		}
		if token, found := selection.noCIRechecks[key]; found {
			o.noCIRecheck[key] = token
		}
		if reviewModes[key] != ports.ReviewWritePreserve {
			o.cacheSetTime(o.Cache.LastReviewPollAt, &o.Cache.lastReviewPollOrder, key, now)
		}
		o.cacheSetTime(o.Cache.LastPRFetchAt, &o.Cache.lastPRFetchOrder, key, now)
	}
	// Per-ref monotonicity (finding #1): build a map of repoKey → candidate
	// PR keys so the final cursor-advance loop can verify that every ref in
	// a repo succeeded before advancing the repo-level ETag/cursor. Without
	// this, a single failed ref followed by a successful observation for a
	// different PR in the same repo restores repoRefreshOK=true and advances
	// the cursor, making the failed update unrecoverable.
	repoCandidateKeys := map[string][]string{}
	for pk := range selection.candidateKeys {
		subj, ok := selection.subjectsByPR[pk]
		if !ok {
			continue
		}
		rk := prKey(subj.repo, 0)
		repoCandidateKeys[rk] = append(repoCandidateKeys[rk], pk)
	}
	for key, ok := range repoRefreshOK {
		if !ok {
			continue
		}
		// The repo ETag/cursor advances only when every candidate ref in
		// that repo has prRefreshOK == true. A single failed ref prevents
		// the cursor from advancing so the failed update is recoverable on
		// the next poll.
		allRefsOK := true
		for _, pk := range repoCandidateKeys[key] {
			if !prRefreshOK[pk] {
				allRefsOK = false
				break
			}
		}
		if !allRefsOK {
			continue
		}
		if etag := repoGuards[key].result.ETag; etag != "" {
			o.cacheSetString(o.Cache.RepoPRListETag, &o.Cache.repoOrder, key, etag)
		}
		// Advance the incremental-discovery cursor only after the repo's PR
		// list was fetched AND all its observations were successfully
		// persisted, so a transient failure does not skip MRs updated during
		// the failed poll
		if listedRepos[key] {
			o.Cache.LastSyncCursor[key] = now
		}
	}
}
