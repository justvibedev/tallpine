package scm

import (
	"context"
	"errors"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// reconcileTerminalGitHubPRs (Item 2) runs a terminal-state reconciliation
// pass for tracked open GitHub PRs that are not in the current state=open
// listing. Such PRs may have transitioned to merged/closed, which GitHub's
// state=open listing permanently drops before their terminal state can be
// observed by the normal refresh path. The pass issues a full detail fetch per
// reconciled PR and routes the result through the normal observation/persistence
// machinery.
//
// The pass runs on every poll where the GitHub repo-list guard is NOT a 304
// (i.e. the listing changed). A "still open" detail result is a no-op
// persistence: the observation is returned but the repository is marked
// refresh-incomplete so the repo ETag and sync cursor do not advance (cross-
// cutting durable-state preservation rule). A terminal (merged/closed) result
// is persisted normally so the terminal transition is observed.
//
// The pass is unbounded — no per-poll cap on reconciliation fetches. The worst
// case (one PR updated, 49 others reconciled) is bounded by the tracked-open-
// PR count, which is small in Tallpine's use case (sessions track the PRs they
// spawned).
//
// ASYMMETRY — this pass is GitHub-only by deliberate design:
//   - GitHub uses state=open on ListPRsByRepo and RepoPRListGuard, so terminal
//     PRs disappear from the listing before their detail can be refreshed.
//     The reviewer asked for tracked-PR terminal reconciliation on GitHub
//     (review Item 2), which state=open requires.
//   - GitLab uses state=all on ListPRsByRepo (approved from the first review,
//     unchanged), so merged/closed MRs remain in the listing and are observed
//     by the normal refresh path. No reconciliation pass is needed for GitLab.
//
// The two providers match what the reviewer requested for each; they are not
// aligned to a single listing strategy.
func (o *Observer) reconcileTerminalGitHubPRs(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState, listedPRs map[string]bool, selection *refreshSelection, now time.Time, markRepoFailed func(ports.SCMRepo)) map[string]ports.SCMObservation {
	out := map[string]ports.SCMObservation{}
	if listedPRs == nil {
		// First poll (no cursor): listedPRs is nil, meaning the full listing
		// was fetched. Tracked open PRs not in the listing already had their
		// chance to be discovered; no reconciliation needed.
		return out
	}
	// Collect refs to reconcile, deduped by repo so we only reconcile per-repo
	// when the repo-list guard was not a 304.
	type pendingReconcile struct {
		ref ports.SCMPRRef
		s   *subject
	}
	var refs []pendingReconcile
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		// Asymmetry: only GitHub needs terminal reconciliation. GitLab uses
		// state=all so terminal MRs never disappear from the listing.
		if s.repo.Provider != "github" {
			continue
		}
		// Only tracked open PRs (non-terminal state in durable storage) are
		// candidates — terminal PRs are not re-fetched since lifecycle already
		// saw the transition.
		if s.known.Merged || s.known.Closed {
			continue
		}
		repoKey := prKey(s.repo, 0)
		g := guards[repoKey]
		// Reconciliation runs only when the repo-list guard is not a 304.
		if g.err != nil || g.result.NotModified {
			continue
		}
		key := keyForTrackedPR(s.known, s.repo)
		// Only reconcile PRs not in the current listing — listed PRs are
		// already covered by the normal refresh path. listedPRs carries the
		// subject key for identity-matched listings and the name key
		// otherwise; check both so a rename cannot double-fetch.
		if listedPRs[key] || listedPRs[prKey(s.repo, s.known.Number)] {
			continue
		}
		// Skip refs already selected as refresh candidates by the normal
		// path (e.g. commit-check ETag changed) — they will be fetched
		// anyway and we must not double-count.
		if selection.candidateKeys[key] {
			continue
		}
		refs = append(refs, pendingReconcile{
			ref: ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL},
			s:   s,
		})
	}
	if len(refs) == 0 {
		return out
	}
	// Unbounded: issue detail fetches for every reconciled PR. The worst case
	// is bounded by the tracked-open-PR count, which is small in Tallpine's use case.
	for _, chunk := range chunks(refs, BatchSize) {
		if err := ctx.Err(); err != nil {
			return out
		}
		// Skip cooled-down providers so a rate-limited GitHub does not
		// suppress reconciliation of other providers (defensive — only
		// GitHub PRs are reconciled here, but the guard keeps the invariant).
		// activePending stays aligned with the refs sent to the provider, so
		// batch[i] is attributed to activePending[i]'s subject positionally.
		activePending := make([]pendingReconcile, 0, len(chunk))
		for _, r := range chunk {
			if o.inRateLimitCooldown(now, r.ref.Repo.Provider) {
				markRepoFailed(r.ref.Repo)
				continue
			}
			activePending = append(activePending, r)
		}
		if len(activePending) == 0 {
			continue
		}
		active := make([]ports.SCMPRRef, len(activePending))
		for i, r := range activePending {
			active[i] = r.ref
		}
		started := time.Now()
		batch, err := o.provider.FetchPullRequests(ctx, active)
		o.logProviderRequest("reconcile_terminal_pull_requests", repoNames(active), started, err)
		if err != nil {
			if cooldown, ok := rateLimitCooldown(now, err); ok {
				for _, ref := range active {
					o.setRateLimitCooldown(now, ref.Repo.Provider, cooldown)
				}
				o.logger.Warn("scm observer: reconciliation rate-limited; entering cooldown", "cooldown", cooldown, "err", err)
			} else {
				o.logger.Error("scm observer: reconciliation detail fetch failed", "err", err)
			}
			for _, ref := range active {
				markRepoFailed(ref.Repo)
			}
			continue
		}
		for i, r := range activePending {
			// A missing or Fetched=false result (transient failure carried as
			// per-observation Error metadata) must not persist and must mark
			// the repo refresh-incomplete so the ETag/cursor do not advance
			// without an observation.
			if i >= len(batch) || !batch[i].Fetched {
				if i < len(batch) && batch[i].Error != nil {
					fetchErr := batch[i].Error
					providerKey := textutil.FirstNonBlank(batch[i].Provider, r.ref.Repo.Provider)
					if errors.Is(fetchErr, ports.ErrSCMNotFound) {
						// Permanent miss, re-observed every tick — Debug, not
						// Warn, so a broken tracked PR does not flood the log.
						o.logger.Debug("scm observer: reconciled PR not found at provider; marking refresh-incomplete", "provider", providerKey, "pr", r.ref.URL, "err", fetchErr)
					} else if cooldown, ok := rateLimitCooldown(now, fetchErr); ok {
						o.setRateLimitCooldown(now, providerKey, cooldown)
						o.logger.Warn("scm observer: reconciliation rate-limited (per-observation); entering cooldown", "provider", providerKey, "cooldown", cooldown, "err", fetchErr)
					} else {
						o.logger.Warn("scm observer: reconciliation fetch failed (per-observation); marking refresh-incomplete", "provider", providerKey, "err", fetchErr)
					}
				}
				markRepoFailed(r.ref.Repo)
				continue
			}
			obs := batch[i]
			obs.ObservedAt = now
			key := keyForTrackedPR(r.s.known, r.s.repo)
			out[key] = obs
			// Register the reconciled PR in selection.subjectsByPR and
			// candidateKeys so the persistence loop processes it. Marking
			// candidateKeys also ensures prRefreshOK is initialized for this
			// key so the commit-ETag cache does not advance unless the
			// persistence succeeds.
			selection.subjectsByPR[key] = r.s
			selection.candidateKeys[key] = true
			// Pre-mark the repo refresh-incomplete for every reconciled
			// observation. A "still open" result is a no-op persistence that
			// must NOT advance the repo ETag or sync cursor (cross-cutting
			// durable-state preservation rule): the persistence loop treats
			// unchanged hashes as a no-op and never clears this mark. If the
			// result IS a terminal transition (hashes changed), a successful
			// write sets prRefreshOK[key]=true, which un-marks the repo so
			// the ETag/cursor can advance on a real terminal transition only.
			markRepoFailed(r.ref.Repo)
		}
	}
	return out
}

func missingLocalState(pr domain.PullRequest) bool {
	return pr.URL == "" || pr.HeadSHA == "" || pr.MetadataHash == "" || pr.CIHash == ""
}
