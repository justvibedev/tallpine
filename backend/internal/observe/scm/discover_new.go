package scm

import (
	"context"
	"errors"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// discoverNewPRs lists each repo's open PRs once and attaches any not-yet-tracked
// PR to the session that owns its source branch. A session owns a PR when the
// PR's source branch equals the session branch or descends from it (the
// "branch/..." stacking convention). One session may therefore pick up several
// PRs (its root plus stacked children). Repos whose PR-list guard reports
// NotModified against a known ETag are skipped, since nothing new can have
// appeared since the last poll.
func (o *Observer) discoverNewPRs(ctx context.Context, sessionRepos []sessionRepo, subjects map[string]*subject, guards map[string]repoGuardState, now time.Time, markRepoFailed func(ports.SCMRepo)) (listedPRs, listedRepos map[string]bool) {
	identities := o.resolveIdentities(ctx, sessionRepos)
	byRepo := map[string][]sessionRepo{}
	repos := map[string]ports.SCMRepo{}
	for _, sr := range sessionRepos {
		key := prKey(sr.repo, 0)
		byRepo[key] = append(byRepo[key], sr)
		repos[key] = sr.repo
	}
	pullsByRepo, listedPRs, listedRepos := o.listRepoPRs(ctx, repos, identities, subjects, guards, markRepoFailed)
	o.adoptDiscoveredPRs(ctx, pullsByRepo, repos, byRepo, identities, subjects, now, markRepoFailed)
	return listedPRs, listedRepos
}

// listRepoPRs lists each repo's updated PRs (incrementally) and records which
// repos and PRs were listed this poll.
func (o *Observer) listRepoPRs(ctx context.Context, repos map[string]ports.SCMRepo, identities map[string]ports.SCMIdentity, subjects map[string]*subject, guards map[string]repoGuardState, markRepoFailed func(ports.SCMRepo)) (pullsByRepo map[string][]ports.SCMPRObservation, listedPRs, listedRepos map[string]bool) {
	// listed tracks which repos had their PR list fetched this poll, and
	// listedPRs tracks which specific PRs were in the updated set. These drive
	// incremental refresh candidate selection so only updated MRs are
	// re-fetched
	listedPRs = map[string]bool{}
	listedRepos = map[string]bool{}
	pullsByRepo = map[string][]ports.SCMPRObservation{}
	for repoKey, repo := range repos {
		if _, ok := identities[identityKey(repo.Provider, repo.Host)]; !ok {
			// Do not acknowledge discoveries we could not attribute. Clear even
			// an older cursor/ETag so recovery retries a full listing after an
			// identity outage longer than the incremental overlap window.
			cacheDelete(o.Cache.RepoPRListETag, &o.Cache.repoOrder, repoKey)
			delete(o.Cache.LastSyncCursor, repoKey)
			if markRepoFailed != nil {
				markRepoFailed(repo)
			}
			continue
		}
		g := guards[repoKey]
		if g.err != nil {
			continue
		}
		if g.result.NotModified && g.hadETag {
			continue
		}
		// Incremental discovery: request only MRs updated since the last
		// successful cursor minus an overlap window. Zero cursor (first poll)
		// requests a full listing
		repoKey := prKey(repo, 0)
		cursor := o.Cache.LastSyncCursor[repoKey]
		updatedAfter := time.Time{}
		if !cursor.IsZero() {
			updatedAfter = cursor.Add(-incrementalDiscoveryOverlap)
		}
		started := time.Now()
		pulls, err := o.provider.ListPRsByRepo(ctx, repo, updatedAfter)
		o.logProviderRequest("list_open_prs", repoFullName(repo), started, err)
		if err != nil {
			o.logger.Debug("scm observer: open PR list failed", "repo", repoFullName(repo), "err", err)
			if markRepoFailed != nil && !errors.Is(err, ports.ErrSCMNotFound) {
				markRepoFailed(repo)
			}
			continue
		}
		pullsByRepo[repoKey] = pulls
		// Record the listed PR numbers so selectRefreshCandidates can
		// restrict refresh to only updated MRs rather than every tracked MR.
		// Subjects are identity-keyed, so when a listed PR's provider id
		// matches a tracked subject, mark the subject key too — otherwise a
		// listing served under a renamed repo's old name would never promote
		// the subject to refresh candidate.
		listedRepos[repoKey] = true
		for _, pr := range pulls {
			listedPRs[prKey(repo, pr.Number)] = true
			if pr.ProviderID != "" {
				if idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID); subjects[idKey] != nil {
					listedPRs[idKey] = true
				}
			}
		}
	}
	return pullsByRepo, listedPRs, listedRepos
}

// adoptDiscoveredPRs persists listed PRs that match a session branch as open
// baseline subjects.
func (o *Observer) adoptDiscoveredPRs(ctx context.Context, pullsByRepo map[string][]ports.SCMPRObservation, repos map[string]ports.SCMRepo, byRepo map[string][]sessionRepo, identities map[string]ports.SCMIdentity, subjects map[string]*subject, now time.Time, markRepoFailed func(ports.SCMRepo)) {
	// Provider-wide identity pre-pass: a legacy tracked row may not have a
	// provider_id yet, while the same PR is listed through both its old and new
	// repository names after a transfer. Associate the stable id from any exact
	// legacy repo#number listing before processing discoveries under either
	// name. Otherwise map iteration order can let the canonical-name listing
	// persist an unknown baseline before the old-name detail fetch stamps the id.
	trackedProviderIdentities := map[string]bool{}
	for repoKey, pulls := range pullsByRepo {
		repo := repos[repoKey]
		for _, pr := range pulls {
			if pr.ProviderID == "" {
				continue
			}
			idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID)
			if subjects[idKey] != nil || subjects[prKey(repo, pr.Number)] != nil {
				trackedProviderIdentities[idKey] = true
			}
		}
	}

	workspaceBranches := map[string]workspaceBranchInfo{}
	for repoKey, pulls := range pullsByRepo {
		repo := repos[repoKey]
		for _, pr := range pulls {
			if pr.Number <= 0 || pr.SourceBranch == "" {
				continue
			}
			id := identities[identityKey(repo.Provider, repo.Host)]
			if !strings.EqualFold(strings.TrimSpace(pr.Author), id.Login) {
				continue
			}
			if _, ok := subjects[prKey(repo, pr.Number)]; ok {
				continue
			}
			// Identity dedupe: a PR already tracked under its provider-native
			// identity must never be treated as new, even when this listing
			// was served under a different repo name (pre-transfer remote,
			// mirror). Without this, the baseline write below re-baselines
			// the tracked row and wipes its CI/mergeability facts and
			// semantic hashes — the "Checking merge readiness" flap
			// (issue #4089, mechanism 2).
			if pr.ProviderID != "" {
				idKey := identityPRKey(repo.Provider, repo.Host, pr.ProviderID)
				if trackedProviderIdentities[idKey] || subjects[idKey] != nil {
					continue
				}
			}
			// Head eligibility includes the registered origin and every configured
			// fetch and push URL, limited to the scanned base's provider and host.
			// Reject unconfigured or deleted heads before matching branch ownership.
			headRepo := pr.HeadRepo
			// Same-repo PR: the head is the listed repo, even if it was renamed.
			if pr.BaseRepo != "" && strings.EqualFold(pr.HeadRepo, pr.BaseRepo) {
				headRepo = repoFullName(repo)
			}
			eligible := candidatesForHeadRepo(byRepo[repoKey], headRepo)
			sr, ok, ambiguous := matchSessionDetail(eligible, pr.SourceBranch)
			if !ok && !ambiguous {
				sr, ok = matchWorkspaceBranch(ctx, eligible, pr.SourceBranch, workspaceBranches)
			}
			if !ok {
				continue
			}
			known := domain.PullRequest{
				URL:          textutil.FirstNonBlank(pr.URL, pr.HTMLURL),
				SessionID:    sr.session.ID,
				Number:       pr.Number,
				Draft:        pr.Draft,
				SourceBranch: pr.SourceBranch,
				TargetBranch: pr.TargetBranch,
				HeadSHA:      pr.HeadSHA,
				Author:       strings.TrimSpace(pr.Author),
				Provider:     repo.Provider,
				Host:         repo.Host,
				Repo:         repoFullName(repo),
				ProviderID:   pr.ProviderID,
				UpdatedAt:    now,
			}
			// Persist the discovered PR as an open baseline row immediately, before
			// the refresh/lifecycle pass runs. A session can own several PRs, and a
			// terminal observation for one of them triggers a completion check that
			// reads every PR of the session from the store. Without this write, an
			// open sibling/child discovered in the same poll would not yet be
			// durable, and the session could terminate while that PR is still open.
			if err := o.store.WriteSCMObservation(ctx, known, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
				o.logger.Error("scm observer: persist discovered PR failed", "session", sr.session.ID, "pr", known.URL, "err", err)
				if markRepoFailed != nil {
					markRepoFailed(repo)
				}
				continue
			}
			subjects[keyForTrackedPR(known, repo)] = &subject{
				session: sr.session,
				repo:    repo,
				branch:  sr.branch,
				known:   known,
				hasPR:   true,
			}
		}
	}
}
