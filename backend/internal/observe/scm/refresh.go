package scm

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

func (o *Observer) selectRefreshCandidates(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState, listedPRs map[string]bool, markRepoFailed func(ports.SCMRepo), now time.Time) refreshSelection {
	selection := refreshSelection{
		subjectsByPR:  map[string]*subject{},
		commitETags:   map[string]pendingCacheString{},
		candidateKeys: map[string]bool{},
		noCIRechecks:  map[string]string{},
	}
	noCI := o.projectNoCILookup()
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		key := keyForTrackedPR(s.known, s.repo)
		selection.subjectsByPR[key] = s
		candidate := missingLocalState(s.known)
		repoCursor := o.Cache.LastSyncCursor[prKey(s.repo, 0)]
		hasCursor := !repoCursor.IsZero()
		g := guards[prKey(s.repo, 0)]
		if g.err == nil && !g.result.NotModified {
			// Incremental discovery: on polls after the first (hasCursor), only
			// refresh MRs that appeared in the updated set. On the first poll
			// (no cursor), refresh all tracked MRs
			if hasCursor && listedPRs != nil {
				if listedPRs[key] {
					candidate = true
				}
			} else {
				candidate = true
			}
		}
		if s.known.HeadSHA != "" {
			commitKey := commitKey(s.repo, s.known.HeadSHA)
			prev := o.Cache.CommitChecksETag[commitKey]
			started := time.Now()
			res, err := o.provider.CommitChecksGuard(ctx, s.repo, s.known.HeadSHA, prev)
			o.logProviderRequest("commit_checks_guard", repoFullName(s.repo), started, err)
			if err != nil {
				o.logger.Error("scm observer: commit check-runs guard failed", "pr", s.known.URL, "sha", s.known.HeadSHA, "err", err)
				if markRepoFailed != nil {
					markRepoFailed(s.repo)
				}
			} else if !res.NotModified && res.ETag != "" && res.ETag != prev {
				// Item 1 — commit-check ETag independent refresh: a changed
				// commit-check ETag promotes this PR to refresh candidate
				// regardless of whether the PR appears in the current
				// repository listing. The previous `listedPRs[key]` gate
				// meant that once a sync cursor existed and the repo-list
				// guard returned 304 (leaving listedPRs empty), CI state
				// changes (pending→passing/failing) were silently dropped.
				// The decoupling ensures CI transitions observed via the
				// commit-check ETag are always persisted. The ETag must
				// actually change (non-empty and different from the cached
				// value) so an empty guard result does not spuriously
				// promote every tracked PR on every poll.
				candidate = true
				selection.commitETags[key] = pendingCacheString{key: commitKey, value: res.ETag}
			}
		}
		if !candidate {
			// Force an unconditional fetch once the snapshot is older than
			// DefaultPRMaxAge. Trade-off: GitHub does not bill 304 responses, so
			// this forgoes the conditional-request savings the ETag path bought.
			// At the current 30s tick / 5m max-age that is ~12 unconditional
			// fetches/hour per tracked open PR, and it scales linearly with the
			// number of tracked PRs. It also fires for every stale PR in the same
			// tick, so watch secondary-rate-limit burstiness as fleets grow.
			//
			// When incremental discovery is active (hasCursor), PRs not in the
			// updated set are left to the terminal-reconciliation pass — do not
			// promote them here or reconciliation will skip them (it checks
			// candidateKeys to avoid double-fetching).
			//
			// A 304 repo-list guard means the listing was not fetched at all, so
			// listedPRs is empty and says nothing about this PR; the fallback
			// must still fire or a moved head (rebase push) stays unseen.
			listSkipped := g.err == nil && g.result.NotModified
			last := o.Cache.LastPRFetchAt[key]
			stale := last.IsZero() || now.Sub(last) > DefaultPRMaxAge
			if !hasCursor || listedPRs == nil || listedPRs[key] || listSkipped {
				if stale {
					candidate = true
					o.logger.Info("scm observer: open PR refresh by max-age", "pr", s.known.URL, "head", s.known.HeadSHA, "repo_list_304", listSkipped, "last_fetch", last)
				}
			} else if stale {
				o.logger.Info("scm observer: open PR refresh skipped, repo list fetched without it", "pr", s.known.URL, "head", s.known.HeadSHA, "last_fetch", last)
			}
		}
		if !candidate {
			if token, stale := o.noCIStateStale(ctx, key, s, noCI); stale {
				candidate = true
				selection.noCIRechecks[key] = token
			}
		}
		if candidate {
			selection.refs = append(selection.refs, ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL})
			selection.refKeys = append(selection.refKeys, key)
			selection.candidateKeys[key] = true
		}
	}
	// Forget noCI recheck tokens of PRs this observer no longer tracks.
	for key := range o.noCIRecheck {
		if _, tracked := selection.subjectsByPR[key]; !tracked {
			delete(o.noCIRecheck, key)
		}
	}
	return selection
}

func (o *Observer) prepareForPersistence(obs ports.SCMObservation, local domain.PullRequest, opts persistenceOptions, now time.Time) ports.SCMObservation {
	metadataHash := metadataSemanticHash(obs)
	if opts.preserveLocalMetadataHash {
		metadataHash = local.MetadataHash
	}
	ciHash := ciSemanticHash(obs.CI)
	if opts.preserveLocalCIHash {
		ciHash = local.CIHash
	}
	reviewHash := local.ReviewHash
	if !opts.preserveLocalReviewHash && (opts.reviewFetched || local.ReviewHash == "" || obs.Review.Decision != string(local.Review)) {
		reviewHash = reviewSemanticHash(obs.Review)
	}
	obs.Changed = ports.SCMChanged{
		Metadata: metadataHash != local.MetadataHash,
		CI:       ciHash != local.CIHash,
		Review:   reviewHash != local.ReviewHash,
	}
	// A successful fetch that changes completeness (partial <-> full) must
	// persist even when the provider content is unchanged: rows upgraded by the
	// conservative review_partial default start uncertain, and without this a
	// content-hash match would skip the write and keep the exact count hidden
	// forever.
	if opts.reviewFetched && obs.Review.Partial != local.ReviewPartial {
		obs.Changed.Review = true
	}
	obs.PR.State = textutil.FirstNonBlank(obs.PR.State, string(domain.PRLifecycleState(obs.PR.Merged, obs.PR.Closed, obs.PR.Draft)))
	obs.ObservedAt = firstTime(obs.ObservedAt, now)
	return obs
}
