package github

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

// githubCheckRunsPageSize fetches the complete check-run set for a commit in a
// single REST page when the commit has no more than this many runs. A commit
// virtually never exceeds GitHub's 100-result cap, so one request carries the
// whole representation and GitHub's ETag reflects every contributing run.
const githubCheckRunsPageSize = 100

// restCheckRunsPage is the GitHub list-check-runs response envelope.
type restCheckRunsPage struct {
	TotalCount int                  `json:"total_count"`
	CheckRuns  []restCommitCheckRun `json:"check_runs"`
}

// restCommitCheckRun is the subset of a GitHub check-run we condition the
// aggregate CI-state guard on.
type restCommitCheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// CommitChecksGuard checks GitHub's per-commit check-runs guard. It conditions
// the fast CI-state guard on the complete check-run set rather than a single
// item: GitHub's ETag for a per_page=1 response only reflects that one run, so
// a different workflow finishing (or failing, or going pending) can leave the
// returned item unchanged and GitHub answers 304 even though the aggregate CI
// state Tallpine displays has changed. Requesting the full page makes the guard
// represent all runs that contribute to the state, and paginating plus
// fingerprinting stays correct when a commit carries more than one page of
// runs. DefaultPRMaxAge remains the safety backstop for PR metadata changes not
// represented by check-state guards.
func (p *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	if strings.TrimSpace(headSHA) == "" {
		return ports.SCMGuardResult{}, fmt.Errorf("%w: empty head sha", ErrNotFound)
	}
	q := url.Values{}
	q.Set("per_page", strconv.Itoa(githubCheckRunsPageSize))
	resp, err := p.clientFor(repo.Host).doRESTWithETag(ctx, repoPath(repo.Owner, repo.Name, "commits", headSHA, "check-runs"), q, etag)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	if resp.NotModified {
		// GitHub confirmed the whole first page is unchanged, which covers every
		// run for the overwhelmingly common single-page case.
		return ports.SCMGuardResult{ETag: cmp.Or(resp.ETag, etag), NotModified: true}, nil
	}
	page, err := decodeRestCheckRunsPage(resp.Body)
	if err != nil {
		// Decode failure is unexpected but shouldn't block the whole repo poll.
		// Return NotModified=false with no ETag so the observer falls back to the
		// full GraphQL refresh path; DefaultPRMaxAge bounds staleness.
		return ports.SCMGuardResult{}, nil //nolint:nilerr // decode failure falls back to the max-age refresh path
	}
	if page.TotalCount > len(page.CheckRuns) {
		// The commit has more runs than one page fits; rely on a stable
		// fingerprint over the complete set instead of a single page's ETag so a
		// transition on a later page still invalidates the guard.
		runs, err := p.fetchRemainingCommitCheckRuns(ctx, repo, headSHA, page)
		if err != nil {
			return ports.SCMGuardResult{}, err
		}
		fp := commitCheckRunsFingerprint(runs)
		if etag == fp {
			return ports.SCMGuardResult{ETag: fp, NotModified: true}, nil
		}
		return ports.SCMGuardResult{ETag: fp}, nil
	}
	// A single page carried the whole representation: GitHub's ETag is the
	// correct validator, so any run transition yields a fresh ETag.
	return ports.SCMGuardResult{ETag: cmp.Or(resp.ETag, etag)}, nil
}

// decodeRestCheckRunsPage unmarshals a GitHub list-check-runs body.
func decodeRestCheckRunsPage(body []byte) (restCheckRunsPage, error) {
	var page restCheckRunsPage
	if err := json.Unmarshal(body, &page); err != nil {
		return restCheckRunsPage{}, fmt.Errorf("github scm: decode check runs: %w", err)
	}
	return page, nil
}

// fetchRemainingCommitCheckRuns gathers the check runs a commit carries beyond
// the first page and returns every run across all pages.
func (p *Provider) fetchRemainingCommitCheckRuns(ctx context.Context, repo ports.SCMRepo, headSHA string, page restCheckRunsPage) ([]restCommitCheckRun, error) {
	runs := append([]restCommitCheckRun(nil), page.CheckRuns...)
	for pageNum := 2; len(runs) < page.TotalCount && pageNum <= githubCheckRunsMaxPages; pageNum++ {
		q := url.Values{}
		q.Set("per_page", strconv.Itoa(githubCheckRunsPageSize))
		q.Set("page", strconv.Itoa(pageNum))
		resp, err := p.clientFor(repo.Host).doREST(ctx, http.MethodGet, repoPath(repo.Owner, repo.Name, "commits", headSHA, "check-runs"), q, nil)
		if err != nil {
			return nil, err
		}
		next, err := decodeRestCheckRunsPage(resp.Body)
		if err != nil {
			return nil, err
		}
		runs = append(runs, next.CheckRuns...)
		if len(next.CheckRuns) == 0 {
			break
		}
	}
	return runs, nil
}

// commitCheckRunsFingerprint is a stable hash of a commit's complete check-run
// set, sorted so the order GitHub returns runs in cannot matter. It only hashes
// the fields that determine the aggregate CI state (name, status, conclusion).
func commitCheckRunsFingerprint(runs []restCommitCheckRun) string {
	parts := make([]string, len(runs))
	for i, r := range runs {
		parts[i] = strings.Join([]string{r.Name, r.Status, r.Conclusion}, "\x00")
	}
	return stableCheckFingerprint(parts)
}

// stableCheckFingerprint computes a stable SHA256 hash of a sorted string slice.
// The slice is sorted, joined with "\x1e", then hashed. This shared logic is
// used by both commitCheckRunsFingerprint and githubFailedFingerprint.
func stableCheckFingerprint(parts []string) string {
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(sum[:])
}

func (p *Provider) fetchRemainingCheckContexts(ctx context.Context, ref ports.SCMPRRef, pr map[string]any) error {
	contexts := statusContexts(pr)
	if contexts == nil {
		return nil
	}
	cursor := pageInfoEndCursor(contexts)
	if cursor == "" {
		return fmt.Errorf("github scm: paginated check contexts for %s#%d missing end cursor", repoFullName(ref.Repo), ref.Number)
	}
	for {
		query := buildCheckContextsQuery(ref, cursor)
		data, err := p.clientFor(ref.Repo.Host).doGraphQL(ctx, query, nil)
		if err != nil {
			return fmt.Errorf("github scm: fetch remaining check contexts for %s#%d: %w", repoFullName(ref.Repo), ref.Number, err)
		}
		repoData, _ := data["repo"].(map[string]any)
		pagePR, _ := repoData["pullRequest"].(map[string]any)
		if pagePR == nil {
			return fmt.Errorf("%w: pull request not found in check context response", ErrNotFound)
		}
		pageContexts := statusContexts(pagePR)
		if pageContexts == nil {
			return fmt.Errorf("github scm: check context fallback for %s#%d returned no contexts", repoFullName(ref.Repo), ref.Number)
		}
		appendStatusContextNodes(contexts, pageContexts)
		if !pageInfoHasMore(pageContexts) {
			break
		}
		cursor = pageInfoEndCursor(pageContexts)
		if cursor == "" {
			return fmt.Errorf("github scm: paginated check context page for %s#%d missing end cursor", repoFullName(ref.Repo), ref.Number)
		}
	}
	return nil
}

func buildCheckContextsQuery(ref ports.SCMPRRef, cursor string) string {
	return fmt.Sprintf(`query{
repo: repository(owner:%s,name:%s){ pullRequest(number:%d){
  commits(last:1){ nodes{ commit{ statusCheckRollup{ contexts(first:%d, after:%s){ nodes{
    __typename
    ... on CheckRun { name status conclusion detailsUrl url databaseId }
    ... on StatusContext { context state targetUrl }
  } pageInfo{ hasNextPage endCursor } } } } } }
} }
}`, graphQLString(ref.Repo.Owner), graphQLString(ref.Repo.Name), ref.Number, scmBatchCheckContextLimit, graphQLString(cursor))
}

func statusContexts(pr map[string]any) map[string]any {
	roll := statusRollup(pr)
	if roll == nil {
		return nil
	}
	contexts, _ := roll["contexts"].(map[string]any)
	return contexts
}

func appendStatusContextNodes(dst, src map[string]any) {
	if dst == nil || src == nil {
		return
	}
	merged, _ := dst["nodes"].([]any)
	for _, n := range nodes(src["nodes"]) {
		merged = append(merged, n)
	}
	dst["nodes"] = merged
	dst["pageInfo"] = src["pageInfo"]
}

func pageInfoEndCursor(connection map[string]any) string {
	pi, _ := connection["pageInfo"].(map[string]any)
	return str(pi["endCursor"])
}

func ciSummaryFromRollupState(pr map[string]any) domain.CIState {
	roll := statusRollup(pr)
	if roll == nil {
		return domain.CIUnknown
	}
	return mapRollupState(str(roll["state"]))
}

// githubHeadHasNoChecks reports whether GitHub returned the PR's head commit
// with no statusCheckRollup at all, i.e. no check runs and no commit statuses
// are registered for it. A missing commit node or a commit other than the head
// is not proof of anything, so it reports false.
func githubHeadHasNoChecks(pr map[string]any) bool {
	if statusRollup(pr) != nil {
		return false
	}
	oid := latestCommitOID(pr)
	if oid == "" {
		return false
	}
	head := str(pr["headRefOid"])
	return head == "" || strings.EqualFold(head, oid)
}

func scmContextsPaginated(pr map[string]any) bool {
	return pageInfoHasMore(statusContexts(pr))
}

func scmChecksFromGraphQL(pr map[string]any) []ports.SCMCheckObservation {
	roll := statusRollup(pr)
	contexts, _ := roll["contexts"].(map[string]any)
	rawNodes := nodes(contexts["nodes"])
	out := make([]ports.SCMCheckObservation, 0, len(rawNodes))
	for _, n := range rawNodes {
		typ := str(n["__typename"])
		var ch ports.SCMCheckObservation
		switch typ {
		case "CheckRun":
			ch.Name = str(n["name"])
			ch.Status = string(checkStatusFromGraphQL(n))
			ch.Conclusion = strings.ToLower(str(n["conclusion"]))
			ch.URL = textutil.FirstNonBlank(str(n["detailsUrl"]), str(n["url"]))
			if id := int64(num(n["databaseId"])); id > 0 {
				ch.ProviderID = strconv.FormatInt(id, 10)
			}
		case "StatusContext":
			ch.Name = str(n["context"])
			ch.Status = string(checkStatusFromGraphQL(n))
			ch.Conclusion = strings.ToLower(str(n["state"]))
			ch.URL = str(n["targetUrl"])
		default:
			continue
		}
		if ch.Name == "" {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func failedSCMChecks(checks []ports.SCMCheckObservation) []ports.SCMCheckObservation {
	out := make([]ports.SCMCheckObservation, 0, len(checks))
	for _, ch := range checks {
		status := domain.PRCheckStatus(ch.Status)
		if status == domain.PRCheckFailed || status == domain.PRCheckCancelled {
			out = append(out, ch)
		}
	}
	return out
}

func githubFailedFingerprint(head string, checks []ports.SCMCheckObservation) string {
	if len(checks) == 0 {
		return ""
	}
	parts := make([]string, len(checks))
	for i, ch := range checks {
		parts[i] = strings.Join([]string{head, ch.Name, ch.Status, ch.Conclusion, ch.URL, ch.ProviderID}, "\x00")
	}
	return stableCheckFingerprint(parts)
}
