package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	ciFailureLogTailLines       = 20
	reviewDiscussionPageSize    = 100
	reviewCommentLimitPerThread = 5
	pipelineJobsPageSize        = 100
	fetchConcurrency            = 5
)

// ---------------------------------------------------------------------------
// ParseRepository
// ---------------------------------------------------------------------------

// ParseRepository parses a Git remote URL and returns an SCMRepo if the host
// belongs to a GitLab instance the provider is configured to trust. gitlab.com
// is always accepted; self-managed hosts must appear in the provider's
// AllowedHosts list. A host that is neither gitlab.com nor allowlisted is
// rejected so credentials are never attached to an untrusted host (review
// Item 5).
//
// Supported remote formats:
//
//   - SSH:   git@gitlab.com:owner/repo.git
//   - SSH:   ssh://git@gitlab.internal:8443/group/repo.git  (port preserved)
//   - HTTPS: https://gitlab.com/owner/repo.git
//   - HTTPS: https://gitlab.internal:8443/group/repo.git      (port preserved)
//
// ssh:// remotes parse to the same host:port as their https:// equivalents
// (review S1).
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	return p.parseGitLabRepo(remote)
}

// ---------------------------------------------------------------------------
// RepoPRListGuard
// ---------------------------------------------------------------------------

// RepoPRListGuard returns an ETag-based guard for the open merge-request list
// of a project, allowing the observer to skip unchanged data.
func (p *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo.Owner, repo.Name))
	q := url.Values{
		"state":    {"opened"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {"1"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}

	resp, err := hc.doRESTWithETag(ctx, path, q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return ports.SCMGuardResult{
		ETag:        resp.ETag,
		NotModified: resp.NotModified,
	}, nil
}

// ---------------------------------------------------------------------------
// ListPRsByRepo
// ---------------------------------------------------------------------------

// ListPRsByRepo lists merge requests in a project, optionally filtered to
// those updated after updatedAfter (zero = full listing). Uses state=all so
// closed/merged MRs are also discovered for state-transition tracking, and
// follows Link rel=next to paginate all pages.
func (p *Provider) ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error) {
	var result []ports.SCMPRObservation
	q := url.Values{
		"state":    {"all"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {"100"},
	}
	if !updatedAfter.IsZero() {
		q.Set("updated_after", updatedAfter.UTC().Format(time.RFC3339Nano))
	}
	path := fmt.Sprintf("/projects/%s/merge_requests", projectPath(repo.Owner, repo.Name))
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, err
	}

	_, err = hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var mrs []restMR
		if err := json.Unmarshal(body, &mrs); err != nil {
			return fmt.Errorf("gitlab scm: unmarshal MR list: %w", err)
		}
		for i := range mrs {
			// Store the full restMR into the MR-detail cache (ticket 03). The
			// list and detail MR responses share the same shape for the fields
			// Tallpine uses, so the cached restMR is a valid substitute for the detail
			// response when fetchSingleMR consults the cache seconds later in the
			// same cycle. diff_refs.base_sha is populated from the list response
			// via the nested-struct field (ticket 02's fix), so the cached entry
			// carries the base SHA correctly. The pointer targets this page's
			// backing array, which is not reused on the next page (each page
			// gets a fresh `mrs` slice), so the cached pointer is safe to retain.
			p.cache.setMRDetail(repo.Host, repo.Repo, mrs[i].IID, &mrs[i])
			result = append(result, mrToSCMPRObservation(repo, &mrs[i]))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// FetchPullRequests
// ---------------------------------------------------------------------------

// FetchPullRequests fetches detailed observations for a batch of merge requests,
// including MR metadata, CI status, approvals, and mergeability.
//
// A transient failure (timeout, 429, 5xx, malformed payload) on any sub-fetch
// is propagated as a non-nil error AND a Fetched=false placeholder observation
// at the same index. The observer rejects Fetched=false observations so the
// last durable state is preserved and ETags/cursors are not advanced (review
// finding #1).
func (p *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) > 25 {
		return nil, fmt.Errorf("gitlab scm: batch size %d exceeds limit 25", len(refs))
	}
	results := make([]ports.SCMObservation, len(refs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, fetchConcurrency)
	var firstErr error

	for i, ref := range refs {
		wg.Add(1)
		go func(idx int, r ports.SCMPRRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			obs, err := p.fetchSingleMR(ctx, r)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				p.logger.Warn("gitlab scm: fetch MR failed", "repo", r.Repo.Repo, "mr", r.Number, "err", err)
				if firstErr == nil {
					firstErr = err
				}
				// Placeholder with Fetched=false so the observer can reject it
				// without advancing durable state. The non-nil return error is
				// the primary signal; Fetched=false is the defensive belt.
				results[idx] = ports.SCMObservation{
					Fetched:  false,
					Provider: "gitlab",
					Host:     r.Repo.Host,
					Repo:     r.Repo.Repo,
					PR:       ports.SCMPRObservation{Number: r.Number, URL: r.URL},
					Error:    err,
				}
				return
			}
			results[idx] = obs
		}(i, ref)
	}
	wg.Wait()
	return results, firstErr
}

func (p *Provider) fetchSingleMR(ctx context.Context, ref ports.SCMPRRef) (ports.SCMObservation, error) {
	repo := ref.Repo
	now := time.Now()

	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMObservation{}, err
	}

	// 1. Fetch MR detail. Consult the MR-detail cache first (ticket 03): if
	// ListPRsByRepo populated a fresh entry for this (host, repo, iid) within
	// the 60s TTL, reuse the cached restMR directly and skip the HTTP GET
	// entirely. On a miss (entry expired, or the MR was not in the listing —
	// e.g. a brand-new MR that appeared between listing and fetch), fall back
	// to the HTTP fetch. GitLab's list and detail MR responses share the same
	// shape for the fields Tallpine uses, so the cached restMR is a valid
	// substitute.
	//
	// diff_refs guard (finding #2): GitLab's project MR listing does not
	// guarantee diff_refs in the response — only the single-MR endpoint
	// documents it. If the cached entry lacks diff_refs.base_sha, fall through
	// to the HTTP GET rather than emitting an empty BaseSHA.
	var mr restMR
	if cached, ok := p.cache.getMRDetail(repo.Host, repo.Repo, ref.Number); ok && cached.DiffRefs.BaseSHA != "" {
		mr = *cached
	} else {
		mrPath := fmt.Sprintf("/projects/%s/merge_requests/%d", projectPath(repo.Owner, repo.Name), ref.Number)
		mrResp, err := hc.doGET(ctx, mrPath, nil)
		if err != nil {
			return ports.SCMObservation{}, err
		}
		if err := json.Unmarshal(mrResp.Body, &mr); err != nil {
			return ports.SCMObservation{}, fmt.Errorf("gitlab scm: unmarshal MR detail: %w", err)
		}
	}

	prObs := mrToSCMPRObservation(repo, &mr)
	canonicalRepo := canonicalRepoFromMRURL(repo.Host, prObs.URL, repo.Repo)
	if mr.SourceProjectID == 0 || mr.SourceProjectID == mr.TargetProjectID {
		prObs.HeadRepo = canonicalRepo
	}
	if requestedURL := strings.TrimSpace(ref.URL); requestedURL != "" && requestedURL != strings.TrimSpace(prObs.URL) {
		prObs.URLAlias = requestedURL
	}
	prObs.BaseSHA = mr.DiffRefs.BaseSHA
	prObs.MergeCommitSHA = mr.MergeCommitSHA

	// Fork MR: resolve the head repository to the source project's
	// path_with_namespace so the head-repository ownership guard validates
	// fork MRs against the correct project (review Item 4). When
	// source_project_id differs from target_project_id, the MR head branch
	// lives in a different project; without resolution, HeadRepo would be the
	// target project and a fork MR with a matching branch name could pass the
	// guard against the wrong project.
	//
	// The source-project path is cached across poll cycles with a 5-min TTL
	// (ticket 05): the path is stable for minutes in practice (it only changes
	// on group rename or project transfer), so across ~10 poll cycles the
	// source-project endpoint is hit once instead of 10 times per fork MR. On
	// failure (404, 5xx, timeout) the error propagates so FetchPullRequests
	// leaves a Fetched=false placeholder with the error attached (fail closed,
	// do not falsify HeadRepo). The cache only short-circuits the happy path;
	// it does not mask failures.
	if mr.SourceProjectID != 0 && mr.SourceProjectID != mr.TargetProjectID {
		path, err := p.fetchSourceProjectCached(ctx, hc, repo.Host, mr.SourceProjectID)
		if err != nil {
			return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch source project %d: %w", mr.SourceProjectID, err)
		}
		if path != "" {
			prObs.HeadRepo = path
		}
	}

	// 2. Fetch CI (pipelines + jobs). A transient failure here must propagate
	// as an error so the observer preserves the last durable CI state rather
	// than overwriting it with "unknown"
	ciObs, err := p.fetchCI(ctx, repo, mr.SHA)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch CI: %w", err)
	}

	// 3. Fetch approvals for review decision. A transient failure here must
	// propagate so the observer preserves the last durable review state
	// rather than overwriting it with "none"
	reviewDecision, err := p.fetchApprovalDecision(ctx, repo, ref.Number)
	if err != nil {
		return ports.SCMObservation{}, fmt.Errorf("gitlab scm: fetch approvals: %w", err)
	}

	// Override the review decision when the MR has requested_changes —
	// see changesRequested for rationale. The approvals endpoint alone
	// does not capture this signal, so we override regardless of approvals.
	if changesRequested(&mr) {
		reviewDecision = domain.ReviewChangesRequest
	}

	// 4. Build mergeability
	mergeObs := mergeabilityFromMR(&mr, ciObs.Summary, string(reviewDecision))

	return ports.SCMObservation{
		Fetched:      true,
		ObservedAt:   now,
		Provider:     "gitlab",
		Host:         repo.Host,
		Repo:         canonicalRepo,
		PR:           prObs,
		CI:           ciObs,
		Review:       ports.SCMReviewObservation{Decision: string(reviewDecision)},
		Mergeability: mergeObs,
	}, nil
}

func canonicalRepoFromMRURL(host, rawURL, fallback string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !strings.EqualFold(u.Host, strings.TrimSpace(host)) {
		return fallback
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 1; i+1 < len(parts); i++ {
		if parts[i] == "-" && parts[i+1] == "merge_requests" {
			return strings.Join(parts[:i], "/")
		}
	}
	return fallback
}

// fetchSourceProjectCached resolves a fork MR source project's
// path_with_namespace, consulting the fork-project cache (5-min TTL, keyed by
// host+source_project_id) before issuing an HTTP request (ticket 05). On a cache
// hit, returns the cached path without an HTTP round-trip. On a miss, fetches
// GET /projects/:source_project_id, caches the path_with_namespace, and returns
// it.
//
// Errors propagate exactly as the uncached fetch did — a 404/5xx/timeout still
// returns the error so the caller fails closed (Fetched=false with the error
// attached, preserving the durable-state rule). The cache only short-circuits
// the happy path; it does not mask failures. A cached empty path is treated as
// a valid resolution (a project with no path is unusual but not a failure), so
// an empty path is stored and returned on subsequent hits.
func (p *Provider) fetchSourceProjectCached(ctx context.Context, hc *Client, host string, sourceProjectID int) (string, error) {
	if path, ok := p.cache.getForkProject(host, sourceProjectID); ok {
		return path, nil
	}
	srcPath := fmt.Sprintf("/projects/%d", sourceProjectID)
	projResp, err := hc.doGET(ctx, srcPath, nil)
	if err != nil {
		return "", err
	}
	var srcProj restProject
	if err := json.Unmarshal(projResp.Body, &srcProj); err != nil {
		return "", fmt.Errorf("gitlab scm: unmarshal source project %d: %w", sourceProjectID, err)
	}
	p.cache.setForkProject(host, sourceProjectID, srcProj.PathWithNamespace)
	return srcProj.PathWithNamespace, nil
}
