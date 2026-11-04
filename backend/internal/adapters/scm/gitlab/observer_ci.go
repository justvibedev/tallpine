package gitlab

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ---------------------------------------------------------------------------
// CommitChecksGuard
// ---------------------------------------------------------------------------

// CommitChecksGuard returns a hash-based guard for the latest pipeline status
// of a commit, allowing the observer to skip unchanged CI state.
func (p *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	if headSHA == "" {
		return ports.SCMGuardResult{}, fmt.Errorf("gitlab scm: empty head SHA: %w", ErrNotFound)
	}
	path := fmt.Sprintf("/projects/%s/pipelines", projectPath(repo.Owner, repo.Name))
	q := url.Values{
		"sha":      {headSHA},
		"per_page": {"1"},
		"order_by": {"id"},
		"sort":     {"desc"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}

	resp, err := hc.doGET(ctx, path, q)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	h := sha256.Sum256(resp.Body)
	newETag := hex.EncodeToString(h[:])
	return ports.SCMGuardResult{
		ETag:        newETag,
		NotModified: etag == newETag,
	}, nil
}

func (p *Provider) fetchCI(ctx context.Context, repo ports.SCMRepo, headSHA string) (ports.SCMCIObservation, error) {
	if headSHA == "" {
		return ports.SCMCIObservation{Summary: string(domain.CIUnknown)}, nil
	}

	path := fmt.Sprintf("/projects/%s/pipelines", projectPath(repo.Owner, repo.Name))
	// per_page=1 matches CommitChecksGuard so both call sites produce the same
	// cache key in Client.doGET's ETag cache. When the guard runs first (the
	// cheap pre-check) and fetchCI runs second, doGET sends If-None-Match and
	// receives a 304 — no body transfer, no JSON parse. Only pipelines[0] (the
	// latest pipeline) is used, so per_page=1 is sufficient. Pipeline-jobs
	// pagination (per_page=100, doGETPaginated) is intentionally untouched.
	q := url.Values{
		"sha":      {headSHA},
		"per_page": {"1"},
		"order_by": {"id"},
		"sort":     {"desc"},
	}
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return ports.SCMCIObservation{}, err
	}

	resp, err := hc.doGET(ctx, path, q)
	if err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("fetch pipelines: %w", err)
	}

	var pipelines []restPipeline
	if err := json.Unmarshal(resp.Body, &pipelines); err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("unmarshal pipelines: %w", err)
	}
	if len(pipelines) == 0 {
		return ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: headSHA, NoChecks: true}, nil
	}

	latest := pipelines[0]
	ciState := pipelineStatusToCI(latest.Status)

	// Fetch jobs from the latest pipeline
	checks, failedChecks, partial, err := p.fetchPipelineJobs(ctx, repo, latest.ID)
	if err != nil {
		return ports.SCMCIObservation{}, fmt.Errorf("fetch pipeline jobs: %w", err)
	}

	fp := gitlabFailedFingerprint(headSHA, failedChecks)

	return ports.SCMCIObservation{
		Summary:           string(ciState),
		HeadSHA:           headSHA,
		FailedFingerprint: fp,
		Checks:            checks,
		FailedChecks:      failedChecks,
		Partial:           partial,
	}, nil
}

type restPipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	SHA    string `json:"sha"`
}

func (p *Provider) fetchPipelineJobs(ctx context.Context, repo ports.SCMRepo, pipelineID int) ([]ports.SCMCheckObservation, []ports.SCMCheckObservation, bool, error) {
	path := fmt.Sprintf("/projects/%s/pipelines/%d/jobs", projectPath(repo.Owner, repo.Name), pipelineID)
	q := url.Values{"per_page": {strconv.Itoa(pipelineJobsPageSize)}}
	var allJobs []restJob
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return nil, nil, false, err
	}

	truncated, err := hc.doGETPaginated(ctx, path, q, func(body []byte) error {
		var jobs []restJob
		if err := json.Unmarshal(body, &jobs); err != nil {
			return fmt.Errorf("unmarshal pipeline jobs: %w", err)
		}
		allJobs = append(allJobs, jobs...)
		return nil
	})
	if err != nil {
		return nil, nil, false, fmt.Errorf("fetch pipeline jobs: %w", err)
	}

	var checks, failed []ports.SCMCheckObservation
	for _, j := range allJobs {
		status := jobStatusToCheckStatus(j.Status)
		check := ports.SCMCheckObservation{
			Name:       j.Name,
			Status:     string(status),
			Conclusion: j.Status,
			URL:        j.WebURL,
			ProviderID: strconv.Itoa(j.ID),
		}
		checks = append(checks, check)
		// Optional failed jobs (allow_failure: true) are classified separately
		// from required failures and must NOT become actionable failed
		// checks when the pipeline succeeds (review Item 13). They still
		// appear in Checks (full CI snapshot) but are not promoted to
		// FailedChecks — their failure is informational, not blocking.
		if isFailingCheckStatus(status) && !j.AllowFailure {
			failed = append(failed, check)
		}
	}
	return checks, failed, truncated, nil
}

type restJob struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	WebURL       string `json:"web_url"`
	AllowFailure bool   `json:"allow_failure"`
}

// ---------------------------------------------------------------------------
// FetchFailedCheckLogTail
// ---------------------------------------------------------------------------

// FetchFailedCheckLogTail returns the last N lines of a failed CI job's trace.
func (p *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	jobID := check.ProviderID
	if jobID == "" {
		return "", fmt.Errorf("gitlab scm: empty job ID")
	}
	path := fmt.Sprintf("/projects/%s/jobs/%s/trace", projectPath(repo.Owner, repo.Name), jobID)
	hc, err := p.clientForRepoErr(repo)
	if err != nil {
		return "", err
	}

	body, err := hc.doGETRaw(ctx, path)
	if err != nil {
		return "", err
	}
	return tailLines(string(body), ciFailureLogTailLines), nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func pipelineStatusToCI(status string) domain.CIState {
	switch status {
	case "success":
		return domain.CIPassing
	case "failed", "canceled":
		return domain.CIFailing
	case "running", "pending", "created", "waiting_for_resource", "preparing", "scheduled":
		return domain.CIPending
	default:
		return domain.CIUnknown
	}
}

func jobStatusToCheckStatus(status string) domain.PRCheckStatus {
	switch status {
	case "success":
		return domain.PRCheckPassed
	case "failed":
		return domain.PRCheckFailed
	case "canceled":
		return domain.PRCheckCancelled
	case "running", "preparing":
		return domain.PRCheckInProgress
	case "pending", "created", "waiting_for_resource", "scheduled":
		return domain.PRCheckQueued
	case "skipped", "manual":
		return domain.PRCheckSkipped
	default:
		return domain.PRCheckUnknown
	}
}

func isFailingCheckStatus(s domain.PRCheckStatus) bool {
	return s == domain.PRCheckFailed || s == domain.PRCheckCancelled
}

func gitlabFailedFingerprint(headSHA string, checks []ports.SCMCheckObservation) string {
	if len(checks) == 0 {
		return ""
	}
	parts := make([]string, len(checks))
	for i, c := range checks {
		parts[i] = headSHA + "\x00" + c.Name + "\x00" + c.Status + "\x00" + c.Conclusion + "\x00" + c.URL + "\x00" + c.ProviderID
	}
	sort.Strings(parts)
	h := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(h[:])
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
