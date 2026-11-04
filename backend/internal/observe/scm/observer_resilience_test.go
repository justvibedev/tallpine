package scm

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestPoll_DoesNotCommitCommitETagWhenFetchFails(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{
		repoGuards:  map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards: map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		fetchErr:    errors.New("graphql down"),
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(400, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after failed fetch: got %q want ci1", got)
	}
}

func TestPoll_LifecycleFailureHoldsBackHashesForDurableRetry(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "old-metadata"
	local.CIHash = "old-ci"
	store.prs["p-1"] = []domain.PullRequest{local}
	changed := testObs(1)
	changed.PR.Title = "changed title"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): changed},
	}
	lc := &fakeLifecycle{err: errors.New("lifecycle down")}
	obs := newTestObserver(store, provider, lc, time.Unix(500, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 {
		t.Fatalf("DB write should succeed before lifecycle retry is queued, got writes=%#v", store.writes)
	}
	if store.writes[0].pr.Title != "changed title" {
		t.Fatalf("DB write did not persist the observed PR state: %#v", store.writes[0].pr)
	}
	if store.writes[0].pr.MetadataHash != local.MetadataHash {
		t.Fatalf("metadata hash advanced before lifecycle acknowledgement: got %q want %q", store.writes[0].pr.MetadataHash, local.MetadataHash)
	}
	if store.writes[0].pr.CIHash != local.CIHash {
		t.Fatalf("CI hash advanced before lifecycle acknowledgement: got %q want %q", store.writes[0].pr.CIHash, local.CIHash)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo1" {
		t.Fatalf("repo ETag advanced after lifecycle failure: got %q want repo1", got)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after lifecycle failure: got %q want ci1", got)
	}

	lc.err = nil
	store.prs["p-1"] = []domain.PullRequest{store.writes[0].pr}
	restarted := newTestObserver(store, provider, lc, time.Unix(501, 0).UTC())
	if err := restarted.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(lc.observed) != 1 {
		t.Fatalf("held-back semantic hashes did not force lifecycle retry after restart: %#v", lc.observed)
	}
	if len(store.writes) != 3 {
		t.Fatalf("retry should write held-back facts then acknowledge hashes, got writes=%d", len(store.writes))
	}
	last := store.writes[len(store.writes)-1].pr
	if last.MetadataHash != metadataSemanticHash(changed) {
		t.Fatalf("metadata hash not acknowledged after lifecycle success: got %q want %q", last.MetadataHash, metadataSemanticHash(changed))
	}
	if last.CIHash != ciSemanticHash(changed.CI) {
		t.Fatalf("CI hash not acknowledged after lifecycle success: got %q want %q", last.CIHash, ciSemanticHash(changed.CI))
	}
}

func TestPoll_WriteFailureDoesNotAdvanceGuardETags(t *testing.T) {
	store := testStoreWithSession()
	store.writeErr = errors.New("db down")
	local := knownPR(1)
	local.MetadataHash = "old-metadata"
	local.CIHash = "old-ci"
	store.prs["p-1"] = []domain.PullRequest{local}
	changed := testObs(1)
	changed.PR.Title = "changed title"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): changed},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(550, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo1" {
		t.Fatalf("repo ETag advanced after write failure: got %q want repo1", got)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after write failure: got %q want ci1", got)
	}
}

func TestPoll_DuplicateTrackedPRKeepsFirstSession(t *testing.T) {
	store := &fakeStore{
		sessions: []domain.SessionRecord{
			{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}},
			{ID: "p-2", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}},
		},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
	pr1 := knownPR(1)
	pr1.MetadataHash = "old-1"
	pr2 := pr1
	pr2.SessionID = "p-2"
	store.prs["p-1"] = []domain.PullRequest{pr1}
	store.prs["p-2"] = []domain.PullRequest{pr2}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(600, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatalf("writes = %d, want exactly one owner write", len(store.writes))
	}
	if store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("duplicate owner overwrote first session: wrote session %s", store.writes[0].pr.SessionID)
	}
}

// TestPoll_StaleOpenPRForcedRefreshAfterMaxAge verifies that an open PR whose
// LastPRFetchAt is zero (never fetched) is re-fetched even when both the repo
// ETag guard and the commit checks guard return NotModified. This covers the
// post-merge stale-ETag scenario where the PR has been merged server-side but
// the provider ETag signals no change.
func TestPoll_StaleOpenPRForcedRefreshAfterMaxAge(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	// Fully-populated open PR — all hashes set, not merged, not closed.
	local.Merged = false
	local.Closed = false
	store.prs["p-1"] = []domain.PullRequest{local}

	// Provider: both guards return NotModified, but the observation reports merged.
	mergedObs := testObs(1)
	mergedObs.PR.Merged = true
	mergedObs.PR.State = "merged"

	repoKey := prKey(testRepo, 0)
	commitCacheKey := commitKey(testRepo, "sha1")

	provider := &fakeProvider{
		repoGuards:  map[string]ports.SCMGuardResult{repoKey: {ETag: "v1", NotModified: true}},
		checkGuards: map[string]ports.SCMGuardResult{commitCacheKey: {ETag: "ci1", NotModified: true}},
		observations: map[string]ports.SCMObservation{
			prKey(testRepo, 1): mergedObs,
		},
	}

	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	// Pre-seed ETags so both guards fire NotModified.
	obs.Cache.RepoPRListETag[repoKey] = "v1"
	obs.Cache.CommitChecksETag[commitCacheKey] = "ci1"
	// Leave LastPRFetchAt empty (zero time) for the PR key — forces refresh.

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("expected 1 fetch batch (stale PR forced refresh), got %d: %#v", len(provider.fetchBatches), provider.fetchBatches)
	}
	var mergedWrite *fakeWrite
	for i := range store.writes {
		if store.writes[i].pr.Number == 1 && store.writes[i].pr.Merged {
			mergedWrite = &store.writes[i]
			break
		}
	}
	if mergedWrite == nil {
		t.Fatalf("expected a write with pr.Merged==true, got writes: %#v", store.writes)
	}

	// Now simulate a recent fetch: set LastPRFetchAt to now and poll again.
	// The PR is now merged in the store, so openTrackedPRs will not include it.
	// Reset the store to still have the open PR so we test the max-age guard specifically.
	store.writes = nil
	provider.fetchBatches = nil
	store.prs["p-1"] = []domain.PullRequest{local} // restore open PR
	obs.Cache.LastPRFetchAt[prKey(testRepo, 1)] = now

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("expected no fetch batch within max-age window, got %d: %#v", len(provider.fetchBatches), provider.fetchBatches)
	}

	// Backdate the last fetch beyond DefaultPRMaxAge. This is the behavior the PR
	// actually adds: the max-age guard (not the never-fetched branch) forces a
	// refetch. The clock is pinned, so parts 1 and 2 only cover last.IsZero() and
	// a zero elapsed time; without this, deleting the `> DefaultPRMaxAge` compare
	// would still pass.
	store.writes = nil
	provider.fetchBatches = nil
	store.prs["p-1"] = []domain.PullRequest{local} // restore open PR
	obs.Cache.LastPRFetchAt[prKey(testRepo, 1)] = now.Add(-(DefaultPRMaxAge + time.Minute))

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("expected forced refresh once older than DefaultPRMaxAge, got %d: %#v", len(provider.fetchBatches), provider.fetchBatches)
	}
}

// TestPoll_RejectsFetchedFalseObservation verifies that a Fetched=false
// observation from a transient provider failure is rejected: the store is not
// written, ETags are not advanced, and lifecycle is not notified (review
// finding #1). The last durable state must be preserved.
func TestPoll_RejectsFetchedFalseObservation(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-metadata"
	local.CIHash = "durable-ci"
	store.prs["p-1"] = []domain.PullRequest{local}

	// Provider returns a Fetched=false placeholder (simulating a transient
	// GitLab 5xx that the adapter converts to Fetched=false + error).
	failedObs := ports.SCMObservation{
		Fetched:  false,
		Provider: "github",
		Host:     "github.com",
		Repo:     "o/r",
		PR:       ports.SCMPRObservation{Number: 1, URL: "https://github.com/o/r/pull/1"},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): failedObs},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(700, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Store must not be written with placeholder facts that would overwrite
	// durable metadata/CI hashes.
	if len(store.writes) != 0 {
		t.Fatalf("store writes = %d, want 0 (Fetched=false must not persist)", len(store.writes))
	}
	// Lifecycle must not be notified with stale/placeholder observations.
	if len(lc.observed) != 0 {
		t.Fatalf("lifecycle observed = %d, want 0 (Fetched=false must not notify)", len(lc.observed))
	}
	// ETags must not advance — the failed observation is not authoritative.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo1" {
		t.Fatalf("repo ETag advanced after Fetched=false: got %q want repo1", got)
	}
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci1" {
		t.Fatalf("commit ETag advanced after Fetched=false: got %q want ci1", got)
	}
}

// returns a rate-limit error, subsequent polls within the cooldown window skip
// that provider's calls instead of hammering it every 30s
func TestPoll_RateLimitCooldown_SkipsProviderCalls(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}

	rle := &testRateLimitError{retryAfter: 2 * time.Minute}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	// First FetchPullRequests returns a rate-limit error; subsequent calls
	// would succeed, but the cooldown must suppress them.
	provider.mu.Lock()
	provider.fetchErr = rle
	provider.mu.Unlock()

	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.fetchBatches == nil || len(provider.fetchBatches) != 1 {
		t.Fatalf("first poll: fetchBatches = %d, want 1", len(provider.fetchBatches))
	}
	if !obs.inRateLimitCooldown(now, "github") {
		t.Fatal("github provider should be in rate-limit cooldown after rate-limit error")
	}

	// Second poll within the cooldown window — provider calls must be skipped.
	provider.mu.Lock()
	provider.fetchErr = nil
	provider.fetchBatches = nil
	provider.repoGuardCalls = 0
	provider.mu.Unlock()
	now2 := now.Add(30 * time.Second)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("cooldown poll: fetchBatches = %d, want 0 (cooldown must skip provider calls)", len(provider.fetchBatches))
	}
}

// TestPoll_RateLimitCooldown_ResumesAfterExpiry verifies that after the
// cooldown expires, the observer resumes calling the provider (review
// finding #4).
func TestPoll_RateLimitCooldown_ResumesAfterExpiry(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}

	rle := &testRateLimitError{retryAfter: 2 * time.Minute}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	provider.mu.Lock()
	provider.fetchErr = rle
	provider.mu.Unlock()

	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// After the cooldown expires, provider calls resume.
	provider.mu.Lock()
	provider.fetchErr = nil
	provider.fetchBatches = nil
	provider.mu.Unlock()
	now2 := now.Add(3 * time.Minute)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) == 0 {
		t.Fatal("after cooldown expiry: fetchBatches = 0, want >=1 (observer must resume)")
	}
}

// TestPoll_PerObservationRateLimitError_TriggersCooldown (Item 7) verifies that
// when the provider returns a Fetched=false observation carrying a rate-limit
// error in its Error field, the observer enters per-provider cooldown for that
// observation's provider. This is the mixed-batch case: the multi dispatcher
// attaches the error as per-observation metadata so a rate-limited GitLab
// alongside a healthy GitHub enters cooldown without suppressing GitHub.
func TestPoll_PerObservationRateLimitError_TriggersCooldown(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}

	rle := &testRateLimitError{retryAfter: 2 * time.Minute}
	provider := &fakeProvider{
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations:   map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
		fetchObsErrors: map[string]error{prKey(testRepo, 1): rle},
	}

	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !obs.inRateLimitCooldown(now, "github") {
		t.Fatal("github provider should be in rate-limit cooldown after per-observation rate-limit error")
	}
	// Subsequent poll within cooldown must skip provider calls.
	provider.mu.Lock()
	provider.fetchObsErrors = nil
	provider.fetchBatches = nil
	provider.mu.Unlock()
	now2 := now.Add(30 * time.Second)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("cooldown poll: fetchBatches = %d, want 0 (cooldown must skip provider calls)", len(provider.fetchBatches))
	}
}

// TestPoll_PerObservationNonRateLimitError_MarksRefreshIncomplete (Item 7)
// verifies that a Fetched=false observation carrying a non-rate-limit error is
// routed to refresh-incomplete, NOT cooldown: the repo ETag and sync cursor
// must not advance, and the provider must not enter cooldown.
func TestPoll_PerObservationNonRateLimitError_MarksRefreshIncomplete(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	store.prs["p-1"] = []domain.PullRequest{local}

	provider := &fakeProvider{
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations:   map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
		fetchObsErrors: map[string]error{prKey(testRepo, 1): errors.New("gitlab 503")},
	}

	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Non-rate-limit errors must NOT enter cooldown.
	if obs.inRateLimitCooldown(now, "github") {
		t.Fatal("github provider must not enter cooldown for a non-rate-limit error")
	}
	// The repo's new ETag ("repo2") must NOT have been cached because the
	// refresh was marked incomplete.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("repo ETag advanced to %q after refresh-incomplete; durable state must not advance", got)
	}
	// The sync cursor must NOT have advanced.
	if cursor := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; !cursor.IsZero() {
		t.Fatalf("sync cursor advanced to %v after refresh-incomplete; durable state must not advance", cursor)
	}
}

// TestPoll_CommitCheckETagChange_PromotesIndependentOfListing (Item 1)
// verifies that a changed commit-check ETag independently promotes a PR to a
// refresh candidate even when the repository listing is a 304 (so listedPRs is
// empty). CI state changes during a 304 listing must be persisted rather than
// silently dropped. This is the reviewer's non-zero-cursor regression test.
func TestPoll_CommitCheckETagChange_PromotesIndependentOfListing(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// New CI observation (pending → passing) that should be persisted.
	updated := testObs(1)
	updated.CI.Summary = string(domain.CIPassing)
	updated.CI.HeadSHA = "sha1"
	updated.CI.Checks = []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}}

	provider := &fakeProvider{
		// Repo-list guard reports 304 (NotModified) against the cached ETag.
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo1", NotModified: true}},
		// Commit-check ETag changed.
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): updated},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1100, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	// Non-zero cursor: simulates the post-first-poll state where a 304 leaves
	// listedPRs empty.
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The PR must be fetched despite the 304 listing.
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("commit-check ETag change with 304 listing must promote PR to refresh candidate; batches=%#v", provider.fetchBatches)
	}
	// The new CI state must be persisted despite the 304 listing.
	if len(store.writes) == 0 {
		t.Fatalf("expected at least one store write with updated CI state, got 0")
	}
	found := false
	for _, w := range store.writes {
		if w.pr.Number == 1 && w.pr.CI == domain.CIPassing {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("updated CI state (passing) not persisted; writes=%#v", store.writes)
	}
	// The commit-check ETag must advance after successful persistence.
	if got := obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")]; got != "ci2" {
		t.Fatalf("commit ETag not advanced after successful persist: got %q want ci2", got)
	}
}

// TestPoll_GitHubTerminalReconciliation_StillOpenNoOp (Item 2) verifies that a
// tracked open GitHub PR not in the current listing triggers a terminal-
// reconciliation detail fetch, and that a "still open" result is a no-op
// persistence that does NOT advance any cursor or ETag.
func TestPoll_GitHubTerminalReconciliation_StillOpenNoOp(t *testing.T) {
	store := testStoreWithSession()
	stillOpen := testObs(1)
	local := knownPR(1)
	// Compute durable hashes from the observation so the "still open" result
	// is semantically a no-op (unchanged hashes → no write, no ETag advance).
	local.MetadataHash = metadataSemanticHash(stillOpen)
	local.CIHash = ciSemanticHash(stillOpen.CI)
	local.ReviewHash = reviewSemanticHash(stillOpen.Review)
	store.prs["p-1"] = []domain.PullRequest{local}

	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{}, // PR not in listing
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): stillOpen},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1200, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The reconciliation pass must issue a detail fetch for the missing PR.
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("reconciliation must fetch the missing PR; batches=%#v", provider.fetchBatches)
	}
	// The repo ETag must NOT advance — the "still open" result is a no-op.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("repo ETag advanced to %q on still-open reconciliation; durable state must not advance", got)
	}
	// The sync cursor must NOT advance.
	if cursor := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; cursor.Equal(now) {
		t.Fatalf("sync cursor advanced to %v on still-open reconciliation; durable state must not advance", cursor)
	}
}

// TestPoll_GitHubTerminalReconciliation_TerminalTransition (Item 2) verifies
// that when the reconciliation detail fetch reports a terminal state
// (merged/closed), the terminal state is observed and persisted.
func TestPoll_GitHubTerminalReconciliation_TerminalTransition(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// The detail fetch returns a merged terminal state.
	terminalObs := testObs(1)
	terminalObs.PR.Merged = true
	terminalObs.PR.State = string(domain.PRStateMerged)
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{}, // PR not in state=open listing
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): terminalObs},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1300, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The reconciliation pass must issue a detail fetch.
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 {
		t.Fatalf("reconciliation must fetch the missing PR; batches=%#v", provider.fetchBatches)
	}
	// The terminal (merged) state must be persisted.
	foundMerged := false
	for _, w := range store.writes {
		if w.pr.Number == 1 && w.pr.Merged {
			foundMerged = true
			break
		}
	}
	if !foundMerged {
		t.Fatalf("terminal merged state not persisted; writes=%#v", store.writes)
	}
	// Lifecycle must see the terminal transition.
	if len(lc.observed) == 0 {
		t.Fatalf("lifecycle not notified of terminal transition; observed=%#v", lc.observed)
	}
}

// TestPoll_GitHubTerminalReconciliation_SecondPollNoReconcile (Item 2)
// verifies the reviewer's explicit multi-poll requirement: after a PR is
// reconciled on poll N (still-open, no-op), poll N+1 does NOT re-reconcile it
// unnecessarily when the listing is unchanged. Once the terminal
// reconciliation finds the PR is still open, the repo ETag has not advanced,
// so a subsequent 304 poll skips reconciliation entirely.
func TestPoll_GitHubTerminalReconciliation_SecondPollNoReconcile(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	stillOpen := testObs(1)
	stillOpen.PR.Title = local.Title
	stillOpen.CI.Summary = string(domain.CIPassing)
	stillOpen.CI.HeadSHA = local.HeadSHA
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): stillOpen},
	}
	now := time.Unix(1400, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)

	// Poll 1: non-304 guard, PR missing from listing → reconciliation fetch.
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("poll 1: reconciliation must fetch the missing PR; batches=%#v", provider.fetchBatches)
	}

	// Poll 2: repo-list guard is now a 304 (NotModified). The PR is still
	// missing from listing, but reconciliation must NOT run on a 304 poll.
	provider.mu.Lock()
	provider.repoGuards[prKey(testRepo, 0)] = ports.SCMGuardResult{ETag: "repo2", NotModified: true}
	provider.fetchBatches = nil
	provider.mu.Unlock()

	now2 := now.Add(time.Minute)
	obs.clock = func() time.Time { return now2 }
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("poll 2 (304 guard): reconciliation must not re-fetch; batches=%#v", provider.fetchBatches)
	}
}

// TestPoll_CooldownSkip_MarksRepoRefreshIncomplete (Item 3) verifies that when
// a ref is skipped under cooldown, the repository is marked refresh-incomplete
// so the repo ETag and sync cursor do NOT advance without an observation being
// fetched. After cooldown, a 304 must not make the skipped update unrecoverable.
func TestPoll_CooldownSkip_MarksRepoRefreshIncomplete(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	now := time.Unix(1500, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	cursorBefore := now.Add(-time.Hour)
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = cursorBefore
	// Put the github provider under cooldown so the ref is skipped.
	obs.setRateLimitCooldown(now, "github", 2*time.Minute)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The ref must not have been fetched (cooldown skipped it).
	if len(provider.fetchBatches) != 0 {
		t.Fatalf("cooldown-skip must not fetch refs; batches=%#v", provider.fetchBatches)
	}
	// The repo ETag must NOT advance — the refresh was marked incomplete.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("repo ETag advanced to %q after cooldown-skip; durable state must not advance", got)
	}
	// The sync cursor must NOT advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; got != cursorBefore {
		t.Fatalf("sync cursor advanced after cooldown-skip: got %v, want %v (unchanged)", got, cursorBefore)
	}
}

// TestPoll_RepoRefreshMonotonicity_OneRefFailsOneSucceeds (finding #1) verifies
// that when two MRs share a repo and one fetch fails while the other succeeds,
// the repo ETag and LastSyncCursor do NOT advance. Without per-ref tracking,
// markRepoRefreshOK (called after the successful PR persistence) would clear
// the repo-level failure set by markRepoRefreshFailed for the failed PR,
// making the failed update unrecoverable.
func TestPoll_RepoRefreshMonotonicity_OneRefFailsOneSucceeds(t *testing.T) {
	store := testStoreWithSession()
	// Two tracked PRs in the same repo with durable hashes.
	pr1, pr2 := knownPR(1), knownPR(2)
	for _, p := range []*domain.PullRequest{&pr1, &pr2} {
		p.MetadataHash = "durable-meta"
		p.CIHash = "durable-ci"
		p.ReviewHash = "durable-review"
	}
	store.prs["p-1"] = []domain.PullRequest{pr1, pr2}

	// PR 1 fetch will fail (per-observation error), PR 2 fetch will succeed.
	pr2Obs := testObs(2)
	pr2Obs.PR.Title = "PR 2 updated" // force a metadata change so it persists
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}, {Number: 2, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 2): pr2Obs},
		// PR 1 returns a Fetched=false placeholder with a non-rate-limit error.
		fetchObsErrors: map[string]error{prKey(testRepo, 1): errors.New("gitlab 503")},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1600, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)
	cursorBefore := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// PR 2 must have been persisted (metadata changed).
	foundPr2Write := false
	for _, w := range store.writes {
		if w.pr.Number == 2 {
			foundPr2Write = true
			break
		}
	}
	if !foundPr2Write {
		t.Fatalf("PR 2 (successful fetch) must be persisted; writes=%#v", store.writes)
	}

	// The repo ETag must NOT advance — PR 1 failed.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("repo ETag advanced to %q when one ref failed; durable state must not advance", got)
	}
	// The sync cursor must NOT advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; got != cursorBefore {
		t.Fatalf("sync cursor advanced when one ref failed: got %v, want %v (unchanged)", got, cursorBefore)
	}
}

// TestPoll_RepoRefreshMonotonicity_BothRefsSucceed (finding #1) verifies
// that when two MRs share a repo and both fetches succeed, the repo ETag and
// LastSyncCursor DO advance.
func TestPoll_RepoRefreshMonotonicity_BothRefsSucceed(t *testing.T) {
	store := testStoreWithSession()
	// Two tracked PRs in the same repo with durable hashes.
	pr1, pr2 := knownPR(1), knownPR(2)
	for _, p := range []*domain.PullRequest{&pr1, &pr2} {
		p.MetadataHash = "durable-meta"
		p.CIHash = "durable-ci"
		p.ReviewHash = "durable-review"
	}
	store.prs["p-1"] = []domain.PullRequest{pr1, pr2}

	// Both PR observations have a metadata change so they persist.
	pr1Obs := testObs(1)
	pr1Obs.PR.Title = "PR 1 updated"
	pr2Obs := testObs(2)
	pr2Obs.PR.Title = "PR 2 updated"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}, {Number: 2, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): pr1Obs, prKey(testRepo, 2): pr2Obs},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1700, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)
	cursorBefore := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Both PRs must have been persisted.
	foundPr1, foundPr2 := false, false
	for _, w := range store.writes {
		if w.pr.Number == 1 {
			foundPr1 = true
		}
		if w.pr.Number == 2 {
			foundPr2 = true
		}
	}
	if !foundPr1 || !foundPr2 {
		t.Fatalf("both PRs must be persisted; writes=%#v", store.writes)
	}

	// The repo ETag must advance.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got != "repo2" {
		t.Fatalf("repo ETag not advanced after all refs succeeded: got %q, want repo2", got)
	}
	// The sync cursor must advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; !got.After(cursorBefore) {
		t.Fatalf("sync cursor not advanced after all refs succeeded: got %v, want > %v", got, cursorBefore)
	}
}

// TestPoll_AllFail_ScopedPerProviderError (Ticket 02) verifies that when ALL
// providers fail in a single chunk, the observer applies the correct error
// classification PER PROVIDER — not one arbitrary classification to all refs.
// GitHub fails with a rate-limit error → GitHub enters per-provider cooldown.
// GitLab fails with an auth error (non-rate-limit) → GitLab is marked
// refresh-incomplete (repo ETag/cursor do not advance) but does NOT enter
// cooldown. The two classifications must not be cross-applied: GitHub must not
// be marked refresh-incomplete-only (missing its cooldown), and GitLab must
// not enter cooldown (wrongly treating an auth error as rate-limit).
//
// This test uses the hostAwareProvider (from scoped_identity_test.go) which
// routes gitlab.com URLs to the gitlab provider key, so the observer sees two
// providers in a single poll.
func TestPoll_AllFail_ScopedPerProviderError(t *testing.T) {
	store := testStoreWithTwoSessions()
	// Two tracked PRs — one GitHub, one GitLab — both with durable hashes.
	ghPR := knownPR(1)
	ghPR.MetadataHash = "durable-meta"
	ghPR.CIHash = "durable-ci"
	ghPR.ReviewHash = "durable-review"
	ghPR.Provider = "github"
	ghPR.Host = "github.com"
	ghPR.Repo = "o/r"

	glPR := knownPR(3)
	glPR.MetadataHash = "durable-meta"
	glPR.CIHash = "durable-ci"
	glPR.ReviewHash = "durable-review"
	glPR.Provider = "gitlab"
	glPR.Host = "gitlab.com"
	glPR.Repo = "o/r"
	glPR.URL = "https://gitlab.com/o/r/-/merge_requests/3"

	store.prs["gh-1"] = []domain.PullRequest{ghPR}
	store.prs["gl-1"] = []domain.PullRequest{glPR}

	ghRateLimitErr := &testRateLimitError{retryAfter: 2 * time.Minute}
	glAuthErr := errors.New("gitlab 401 unauthorized")

	provider := &hostAwareProvider{fakeProvider: &fakeProvider{
		identity: ports.SCMIdentity{Login: "alice", Human: true},
		repoGuards: map[string]ports.SCMGuardResult{
			prKey(testRepo, 0): {ETag: "repo2"},
			prKey(glRepo, 0):   {ETag: "repo2"},
		},
		openPRs: map[string][]ports.SCMPRObservation{
			prKey(testRepo, 0): {{Number: 1, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}},
			prKey(glRepo, 0):   {{Number: 3, State: "open", SourceBranch: "feat", HeadRepo: "o/r"}},
		},
		// Both PRs fail: GitHub with rate-limit, GitLab with auth error.
		fetchObsErrors: map[string]error{
			prKey(testRepo, 1): ghRateLimitErr,
			prKey(glRepo, 3):   glAuthErr,
		},
	}}

	now := time.Unix(2000, 0).UTC()
	obs := New(provider, store, &fakeLifecycle{}, Config{
		Clock:                  func() time.Time { return now },
		Tick:                   time.Hour,
		Logger:                 quietSlog(),
		CacheMax:               128,
		ScopedIdentityResolver: provider.fakeProvider,
	})
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.RepoPRListETag[prKey(glRepo, 0)] = "repo1"
	ghCursorBefore := now.Add(-time.Hour)
	glCursorBefore := now.Add(-time.Hour)
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = ghCursorBefore
	obs.Cache.LastSyncCursor[prKey(glRepo, 0)] = glCursorBefore

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// GitHub must be in rate-limit cooldown (rate-limit error classified correctly).
	if !obs.inRateLimitCooldown(now, "github") {
		t.Fatal("github provider must be in rate-limit cooldown after rate-limit error")
	}

	// GitLab must NOT be in cooldown (auth error is non-rate-limit).
	if obs.inRateLimitCooldown(now, "gitlab") {
		t.Fatal("gitlab provider must NOT enter cooldown for a non-rate-limit (auth) error")
	}

	// Both repos must be marked refresh-incomplete: GitHub via cooldown-skip,
	// GitLab via per-observation non-rate-limit routing. The ETags must NOT
	// have advanced to "repo2".
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("github repo ETag advanced to %q after all-fail; durable state must not advance", got)
	}
	if got := obs.Cache.RepoPRListETag[prKey(glRepo, 0)]; got == "repo2" {
		t.Fatalf("gitlab repo ETag advanced to %q after all-fail; durable state must not advance", got)
	}

	// Neither sync cursor must advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; got != ghCursorBefore {
		t.Fatalf("github sync cursor advanced after all-fail: got %v, want %v (unchanged)", got, ghCursorBefore)
	}
	if got := obs.Cache.LastSyncCursor[prKey(glRepo, 0)]; got != glCursorBefore {
		t.Fatalf("gitlab sync cursor advanced after all-fail: got %v, want %v (unchanged)", got, glCursorBefore)
	}

	// No PRs should have been persisted (all failed).
	for _, w := range store.writes {
		if w.pr.Number == 1 || w.pr.Number == 3 {
			t.Fatalf("failed PR %d must not be persisted; writes=%#v", w.pr.Number, store.writes)
		}
	}
}

// TestPoll_RepoRefreshMonotonicity_ListingFailsButPRSucceeds (finding #1)
// verifies that when ListPRsByRepo fails for a repo but a tracked PR in that
// repo is still fetched and persisted successfully (via the commit-check ETag
// path), the repo ETag and LastSyncCursor do NOT advance. Without the
// repoListFailed monotonicity guard, markRepoRefreshOK (called after the
// successful PR persistence) would flip the repo back to OK. The per-ref
// monotonicity check would then see an empty repoCandidateKeys (because the
// listing failed, the PR never entered candidateKeys through the listing path)
// and allRefsOK would be vacuously true, advancing the ETag/cursor and making
// the failed listing unrecoverable on the next poll.
func TestPoll_RepoRefreshMonotonicity_ListingFailsButPRSucceeds(t *testing.T) {
	store := testStoreWithSession()
	// A tracked PR with durable hashes.
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// The PR observation has a metadata change so it persists.
	successObs := testObs(1)
	successObs.PR.Title = "PR 1 updated"
	provider := &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		// Listing fails — the PR is not discovered through the listing path.
		listErr: errors.New("gitlab 502"),
		// A changed commit-check ETag promotes the PR to a refresh candidate
		// even though the listing failed.
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, local.HeadSHA): {ETag: "checks2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): successObs},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1800, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)
	cursorBefore := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]
	// Seed the commit-check ETag so the guard reports a change.
	obs.Cache.CommitChecksETag[commitKey(testRepo, local.HeadSHA)] = "checks1"

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The PR must have been persisted (metadata changed).
	foundWrite := false
	for _, w := range store.writes {
		if w.pr.Number == 1 {
			foundWrite = true
			break
		}
	}
	if !foundWrite {
		t.Fatalf("PR 1 (successful fetch via commit-check ETag) must be persisted; writes=%#v", store.writes)
	}

	// The repo ETag must NOT advance — the listing failed.
	if got := obs.Cache.RepoPRListETag[prKey(testRepo, 0)]; got == "repo2" {
		t.Fatalf("repo ETag advanced to %q when listing failed; durable state must not advance", got)
	}
	// The sync cursor must NOT advance.
	if got := obs.Cache.LastSyncCursor[prKey(testRepo, 0)]; got != cursorBefore {
		t.Fatalf("sync cursor advanced when listing failed: got %v, want %v (unchanged)", got, cursorBefore)
	}
}

// After a repository rename or org transfer, the provider serves fetches for
// the old owner/name through a redirect: the tracked subject still carries the
// stale repo recorded on the stored PR row, while the fetched observation
// canonicalizes to the new owner/name. These tests pin that such observations
// are dispatched under the tracked subject's key (and persisted with the
// canonical repo so the identities converge) instead of being dropped, which
// left CI/mergeability permanently "unknown" while review stayed fresh.

func TestPoll_FailedFirstReviewFetchDoesNotManufactureCompleteness(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	// The PR has never had a successful review fetch. A metadata change in the
	// same poll must not turn that into a complete-looking review observation:
	// the summary gate treats (zero timestamp, non-partial) as unknown, and a
	// manufactured timestamp would publish a known-looking zero count.
	local.ReviewObservedAt = time.Time{}
	store.prs["p-1"] = []domain.PullRequest{local}
	obsValue := testObs(1)
	obsValue.PR.Title = "PR (updated)"
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue},
		reviewErr:    errors.New("review window unavailable"),
	}
	obs := newTestObserver(store, provider, nil, time.Unix(300, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 {
		t.Fatal("metadata change should still be persisted")
	}
	written := store.writes[0].pr
	if written.Title != "PR (updated)" {
		t.Fatalf("metadata change not persisted: %+v", written)
	}
	if !written.ReviewObservedAt.IsZero() {
		t.Fatalf("failed first review fetch manufactured a review observation at %v; only a successful fetch may establish one", written.ReviewObservedAt)
	}
	if written.ReviewPartial {
		t.Fatalf("never-fetched review storage must stay non-partial (the zero timestamp carries the unknown): %+v", written)
	}
}
