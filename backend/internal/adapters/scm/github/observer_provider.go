package github

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const scmBatchCheckContextLimit = 20

// scmBatchPathLimit caps the changed paths fetched per PR for the merge digest.
const scmBatchPathLimit = 100

const (
	// githubReviewThreadPageSize fetches the latest review window cheaply for
	// the common case while still covering active review feedback.
	githubReviewThreadPageSize = 50
	// githubReviewCommentLimitPerThread stores only the leading comments needed
	// to understand a thread without making one pathological thread dominate
	// GraphQL cost.
	githubReviewCommentLimitPerThread = 5
	// githubReviewThreadMaxPages bounds the explicit older-thread fallback.
	githubReviewThreadMaxPages = 2
	// githubReviewSummaryLimit bounds submitted decisive reviews used for summary links.
	githubReviewSummaryLimit = 20
	// githubCheckRunsMaxPages bounds the pagination when fetching all check runs
	// for a commit. This prevents unbounded pagination on malformed responses.
	githubCheckRunsMaxPages = 10
)

// ParseRepository normalizes a GitHub remote/origin URL into a provider-neutral
// repository key. It accepts https://github.com/owner/repo(.git),
// git@github.com:owner/repo(.git), and path-only owner/repo inputs used by tests.
func (p *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	repo, ok := parseGitHubRepo(remote)
	return repo, ok
}

// RepoPRListGuard checks GitHub's cheap open-PR-list ETag guard.
func (p *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	q := url.Values{}
	q.Set("state", "open")
	q.Set("sort", "updated")
	q.Set("direction", "desc")
	q.Set("per_page", "1")
	resp, err := p.clientFor(repo.Host).doRESTWithETag(ctx, repoPath(repo.Owner, repo.Name, "pulls"), q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return ports.SCMGuardResult{ETag: cmp.Or(resp.ETag, etag), NotModified: resp.NotModified}, nil
}

// ListPRsByRepo lists pull requests in the repository, optionally filtered to
// those updated after updatedAfter (zero = full listing). It paginates the REST
// pulls endpoint using state=open and sort=updated
func (p *Provider) ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error) {
	const perPage = 100
	out := []ports.SCMPRObservation{}
	for page := 1; ; page++ {
		q := url.Values{}
		q.Set("state", "open")
		q.Set("sort", "updated")
		q.Set("direction", "desc")
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		if !updatedAfter.IsZero() {
			q.Set("since", updatedAfter.UTC().Format(time.RFC3339Nano))
		}
		resp, err := p.clientFor(repo.Host).doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "pulls"), q, nil)
		if err != nil {
			return nil, err
		}
		var pulls []restListPull
		if err := json.Unmarshal(resp.Body, &pulls); err != nil {
			return nil, fmt.Errorf("github scm: decode open PR list: %w", err)
		}
		for _, pull := range pulls {
			out = append(out, restListPullToSCM(pull))
		}
		if len(pulls) < perPage {
			return out, nil
		}
	}
}

// FetchPullRequests fetches normalized PR/check metadata for up to 25 PR refs
// in one GraphQL request, positionally aligned with refs: out[i] answers
// refs[i]. A PR the batch query could not resolve (deleted repo, permissions,
// an old name recreated so the redirect no longer lands) leaves a
// Fetched=false placeholder carrying ErrNotFound at its index, so the
// observer marks that ref refresh-incomplete and can log the permanent miss
// distinctly from a transient provider failure. The observer owns chunking;
// this method rejects larger batches so tests catch accidental over-batching.
func (p *Provider) fetchPullRequestsBatch(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if len(refs) > 25 {
		return nil, fmt.Errorf("github scm: batch size %d exceeds 25", len(refs))
	}
	query, aliases := buildSCMBatchQuery(refs)
	data, err := p.clientFor(refs[0].Repo.Host).doGraphQL(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ports.SCMObservation, len(refs))
	for i, ref := range refs {
		repoData, _ := data[aliases[i]].(map[string]any)
		pr, _ := repoData["pullRequest"].(map[string]any)
		if pr == nil {
			out[i] = ports.SCMObservation{
				Fetched:  false,
				Provider: ref.Repo.Provider,
				Host:     ref.Repo.Host,
				Repo:     repoFullName(ref.Repo),
				PR:       ports.SCMPRObservation{Number: ref.Number, URL: ref.URL},
				Error:    fmt.Errorf("%w: pull request %s#%d not in batch response", ErrNotFound, repoFullName(ref.Repo), ref.Number),
			}
			continue
		}
		if scmContextsPaginated(pr) {
			if err := p.fetchRemainingCheckContexts(ctx, ref, pr); err != nil {
				return nil, err
			}
		}
		out[i] = scmObservationFromGraphQL(ref, pr)
	}
	return out, nil
}

// FetchFailedCheckLogTail fetches and tails a failed GitHub Actions job log.
func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	if check.ProviderID == "" {
		return "", nil
	}
	jobID, err := strconv.ParseInt(check.ProviderID, 10, 64)
	if err != nil {
		return "", fmt.Errorf("github scm: parse check provider id %q: %w", check.ProviderID, err)
	}
	if jobID <= 0 {
		return "", nil
	}
	log, err := p.fetchJobLogTail(ctx, repo.Host, repo.Owner, repo.Name, jobID)
	if err != nil {
		return "", err
	}
	return tailLines(log, ciFailureLogTailLines), nil
}

// FetchReviewThreads fetches review threads separately from the fast PR/CI path.
func (p *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	ctx = p.repoCtx(ctx, ref.Repo.Host, ref.Repo.Owner, ref.Repo.Name)
	latest, reviews, decision, pi, err := p.fetchReviewThreadPage(ctx, ref, "", true)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	if !boolv(pi["hasPreviousPage"]) {
		return ports.SCMReviewObservation{Decision: decision, Reviews: reviews, Threads: latest}, nil
	}
	out := latest
	startCursor := str(pi["startCursor"])
	// GitHub returns nodes in connection order even when selecting last:N, so
	// latest[0] is the oldest thread in the latest window. If that boundary
	// thread is still unresolved, fetch one older window to avoid hiding older
	// active review feedback behind the normal 50-thread cost cap.
	oldestLatestUnresolved := len(latest) == 0 || !latest[0].Resolved
	if oldestLatestUnresolved {
		if startCursor == "" {
			p.logger.Warn("github scm: review thread page is partial but missing start cursor",
				"repo", repoFullName(ref.Repo), "pr", ref.Number)
		} else {
			older, _, _, olderPI, err := p.fetchReviewThreadPage(ctx, ref, startCursor, false)
			if err != nil {
				return ports.SCMReviewObservation{}, err
			}
			combined := make([]ports.SCMReviewThreadObservation, 0, len(older)+len(latest))
			combined = append(combined, older...)
			combined = append(combined, latest...)
			out = combined
			if boolv(olderPI["hasPreviousPage"]) {
				p.logger.Warn("github scm: review thread page limit reached",
					"repo", repoFullName(ref.Repo), "pr", ref.Number,
					"max_pages", githubReviewThreadMaxPages)
			}
		}
	}
	return ports.SCMReviewObservation{Decision: decision, Reviews: reviews, Threads: out, Partial: true}, nil
}
