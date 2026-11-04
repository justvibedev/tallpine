package scm

// This file tests the SCM observer orchestration contract with fake provider,
// store, and lifecycle collaborators so ETag decisions, batching, log fetching,
// review cadence, semantic hashes, and notification behavior stay provider-neutral.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var (
	testRepo    = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}
	testAPIRepo = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "api", Repo: "o/api"}
)

type fakeStore struct {
	mu sync.Mutex

	sessions       []domain.SessionRecord
	projects       map[string]domain.ProjectRecord
	workspaceRepos map[string][]domain.WorkspaceRepoRecord
	prs            map[domain.SessionID][]domain.PullRequest
	checks         map[string][]domain.PullRequestCheck
	writeErr       error

	writes []fakeWrite

	listEntered chan struct{}
	listRelease chan struct{}
}

type fakeWrite struct {
	pr         domain.PullRequest
	checks     []domain.PullRequestCheck
	reviews    []domain.PullRequestReview
	threads    []domain.PullRequestReviewThread
	comments   []domain.PullRequestComment
	reviewMode ports.ReviewWriteMode
}

// ListLiveSessions mirrors the store's WHERE is_terminated = 0.
func (s *fakeStore) ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	if s.listEntered != nil {
		select {
		case <-s.listEntered:
		default:
			close(s.listEntered)
		}
	}
	if s.listRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.listRelease:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.SessionRecord
	for _, r := range s.sessions {
		if !r.IsTerminated {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	return p, ok, nil
}

func (s *fakeStore) UpsertProject(_ context.Context, row domain.ProjectRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.projects == nil {
		s.projects = map[string]domain.ProjectRecord{}
	}
	s.projects[row.ID] = row
	return nil
}

func (s *fakeStore) ListWorkspaceRepos(_ context.Context, projectID string) ([]domain.WorkspaceRepoRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.WorkspaceRepoRecord(nil), s.workspaceRepos[projectID]...), nil
}

func (s *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequest(nil), s.prs[id]...), nil
}

func (s *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequestCheck(nil), s.checks[prURL]...), nil
}

func (s *fakeStore) WriteSCMObservation(_ context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, reviewMode ports.ReviewWriteMode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.writeErr != nil {
		return s.writeErr
	}
	s.writes = append(s.writes, fakeWrite{pr: pr, checks: append([]domain.PullRequestCheck(nil), checks...), reviews: append([]domain.PullRequestReview(nil), reviews...), threads: append([]domain.PullRequestReviewThread(nil), threads...), comments: append([]domain.PullRequestComment(nil), comments...), reviewMode: reviewMode})
	return nil
}

type fakeProvider struct {
	mu           sync.Mutex
	repoGuards   map[string]ports.SCMGuardResult
	checkGuards  map[string]ports.SCMGuardResult
	openPRs      map[string][]ports.SCMPRObservation
	listErr      error
	guardErr     error
	observations map[string]ports.SCMObservation
	reviews      map[string]ports.SCMReviewObservation
	logTails     map[string]string
	fetchErr     error
	reviewErr    error

	// fetchObsErrors maps a prKey to a per-observation error that the fake
	// attaches to the observation it returns for that ref, mimicking the
	// multi dispatcher's per-provider Error metadata. When set, the returned
	// observation has Fetched=false + Error=err so the observer can route it.
	fetchObsErrors map[string]error

	credentialGate   bool
	credentialOK     bool
	credentialErr    error
	credentialChecks int
	repoGuardCalls   int
	listCalls        int
	fetchBatches     [][]ports.SCMPRRef
	logCalls         int
	reviewCalls      int
	identity         ports.SCMIdentity
	identityErr      error
	identityCalls    int
}

type fakeIdentityResolver struct {
	identity ports.SCMIdentity
	err      error
	calls    int
}

func (r *fakeIdentityResolver) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	r.calls++
	return r.identity, r.err
}

func (p *fakeProvider) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.identityCalls++
	return p.identity, p.identityErr
}

func (p *fakeProvider) SCMCredentialsAvailable(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentialChecks++
	if !p.credentialGate {
		return true, nil
	}
	return p.credentialOK, p.credentialErr
}

func (p *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ports.SCMRepo{}, false
	}
	s := strings.TrimSuffix(remote, ".git")
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "git@github.com:")
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: parts[0], Name: parts[1], Repo: parts[0] + "/" + parts[1]}, true
}
func (p *fakeProvider) RepoPRListGuard(_ context.Context, repo ports.SCMRepo, _ string) (ports.SCMGuardResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repoGuardCalls++
	if p.guardErr != nil {
		return ports.SCMGuardResult{}, p.guardErr
	}
	return p.repoGuards[prKey(repo, 0)], nil
}
func (p *fakeProvider) ListPRsByRepo(_ context.Context, repo ports.SCMRepo, _ time.Time) ([]ports.SCMPRObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listCalls++
	if p.listErr != nil {
		return nil, p.listErr
	}
	return p.openPRs[prKey(repo, 0)], nil
}
func (p *fakeProvider) CommitChecksGuard(_ context.Context, repo ports.SCMRepo, sha, _ string) (ports.SCMGuardResult, error) {
	return p.checkGuards[commitKey(repo, sha)], nil
}

// FetchPullRequests honors the positional-alignment contract: out[i] answers
// refs[i], with a zero-value Fetched=false placeholder for refs the fake has
// no observation for.
func (p *fakeProvider) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetchBatches = append(p.fetchBatches, append([]ports.SCMPRRef(nil), refs...))
	if p.fetchErr != nil {
		return nil, p.fetchErr
	}
	out := make([]ports.SCMObservation, len(refs))
	for i, ref := range refs {
		key := prKey(ref.Repo, ref.Number)
		// Per-observation error mimics the multi dispatcher's per-provider
		// Error metadata: a Fetched=false placeholder carrying the failure
		// so the observer can route it to cooldown / refresh-incomplete.
		if err, ok := p.fetchObsErrors[key]; ok {
			out[i] = ports.SCMObservation{
				Fetched:  false,
				Provider: ref.Repo.Provider,
				Host:     ref.Repo.Host,
				Repo:     ref.Repo.Repo,
				PR:       ports.SCMPRObservation{Number: ref.Number, URL: ref.URL},
				Error:    err,
			}
			continue
		}
		if obs, ok := p.observations[key]; ok {
			out[i] = obs
		}
	}
	return out, nil
}
func (p *fakeProvider) FetchFailedCheckLogTail(_ context.Context, _ ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logCalls++
	return p.logTails[check.Name], nil
}
func (p *fakeProvider) FetchReviewThreads(_ context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviewCalls++
	if p.reviewErr != nil {
		return ports.SCMReviewObservation{}, p.reviewErr
	}
	return p.reviews[prKey(ref.Repo, ref.Number)], nil
}

type fakeLifecycle struct {
	observed []ports.SCMObservation
	err      error
}

func (l *fakeLifecycle) ApplySCMObservation(_ context.Context, _ domain.SessionID, obs ports.SCMObservation) error {
	if l.err != nil {
		return l.err
	}
	l.observed = append(l.observed, obs)
	return nil
}

func newTestObserver(store *fakeStore, provider *fakeProvider, lc Lifecycle, now time.Time) *Observer {
	if provider.identity.Login == "" && provider.identityErr == nil {
		provider.identity = ports.SCMIdentity{Login: "alice", Human: true}
	}
	for repo, prs := range provider.openPRs {
		for i := range prs {
			if prs[i].Author == "" {
				prs[i].Author = "alice"
			}
		}
		provider.openPRs[repo] = prs
	}
	return New(provider, store, lc, Config{Clock: func() time.Time { return now }, Tick: time.Hour, Logger: quietSlog(), CacheMax: 128, ScopedIdentityResolver: provider})
}

func quietSlog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testStoreWithSession() *fakeStore {
	return &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
}

func testObs(num int) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "o/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/" + fmt.Sprint(num), Number: num, State: "open", SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha" + fmt.Sprint(num), Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha" + fmt.Sprint(num), Checks: []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}}},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

func knownPR(num int) domain.PullRequest {
	obs := testObs(num)
	pr, _, _, _, _ := domainFromObservation("p-1", domain.SessionRecord{AutoInjectReview: true}, obs, domain.PullRequest{}, persistenceOptions{}, time.Unix(1, 0).UTC())
	// A known PR has been previously observed — simulate a prior review fetch
	// so needsReviewRefresh does not fire solely because ReviewObservedAt is zero.
	pr.ReviewObservedAt = time.Unix(2, 0).UTC()
	return pr
}

type countingBranchStates struct{ calls int }

func (c *countingBranchStates) ReconcileBranchStates(context.Context) error {
	c.calls++
	return nil
}

// syncBuffer is a goroutine-safe wrapper around bytes.Buffer for capturing
// slog output emitted from the observer's background goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func mustGit(t *testing.T, args ...string) {
	t.Helper()
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, out)
	}
}

// testRateLimitError is a minimal rate-limit error satisfying the observer's
// rateLimitedError interface (GetRetryAfter / GetResetAt) via errors.As.
type testRateLimitError struct {
	retryAfter time.Duration
}

func (e *testRateLimitError) Error() string { return "test: rate limited" }

func (e *testRateLimitError) GetRetryAfter() time.Duration { return e.retryAfter }

func (e *testRateLimitError) GetResetAt() time.Time { return time.Time{} }

func renamedRepoFixture() (*fakeStore, *fakeProvider, ports.SCMRepo) {
	oldRepo := ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "old", Name: "r", Repo: "old/r"}
	store := &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/old/r.git"}},
		prs: map[domain.SessionID][]domain.PullRequest{"p-1": {{
			URL: "https://github.com/new/r/pull/1", SessionID: "p-1", Number: 1,
			Provider: "github", Host: "github.com", Repo: "old/r", SourceBranch: "feat",
		}}},
		checks: map[string][]domain.PullRequestCheck{},
	}
	canonical := ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "new/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/new/r/pull/1", Number: 1, State: "open", SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha1", Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha1"},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewRequired)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"review_required"}},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(oldRepo, 0): {ETag: "v1"}},
		openPRs:      map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{prKey(oldRepo, 1): canonical},
	}
	return store, provider, oldRepo
}

type fakeReviewRuns []domain.ReviewRun

func (f fakeReviewRuns) ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	return f, nil
}

// quietNoCIPoll sets up a poll in which nothing provider-side changed: the
// repo listing and commit checks are 304 and the PR was fetched recently, so
// only the noCI staleness rule can make the stored PR a refresh candidate.
func quietNoCIPoll(t *testing.T, noCI bool, local domain.PullRequest, storedChecks []domain.PullRequestCheck, fetched ports.SCMObservation, runs fakeReviewRuns) (*Observer, *fakeStore, *fakeProvider) {
	t.Helper()
	store := testStoreWithSession()
	p := store.projects["p"]
	p.Config.NoCI = noCI
	store.projects["p"] = p
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = storedChecks
	now := time.Unix(100, 0).UTC()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, local.HeadSHA): {ETag: "ci", NotModified: true}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): fetched},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.reviewRuns = runs
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, local.HeadSHA)] = "ci"
	obs.Cache.LastPRFetchAt[keyForTrackedPR(local, testRepo)] = now
	return obs, store, provider
}
