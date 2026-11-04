package scm

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestPoll_CIETagChangeRefreshesWhenRepoUnchanged(t *testing.T) {
	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(2, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	pollOnce(t, obs)
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("CI ETag 200 should trigger batch fetch, got %d", len(provider.fetchBatches))
	}
}

func TestPoll_FailingCIFetchesLogTailOnlyWhenFingerprintChanged(t *testing.T) {
	failing := testObs(1)
	failing.CI.Summary = string(domain.CIFailing)
	failing.CI.Checks = []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckFailed), Conclusion: "failure", ProviderID: "99"}}
	failing.CI.FailedChecks = failing.CI.Checks
	failing.CI.FailedFingerprint = "fp"

	store := testStoreWithSession()
	local := knownPR(1)
	local.CIHash = "old"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, checkGuards: map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): failing}, logTails: map[string]string{"build": "tail"}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	pollOnce(t, obs)
	if provider.logCalls != 1 {
		t.Fatalf("log calls = %d, want 1", provider.logCalls)
	}

	provider.logCalls = 0
	store.writes = nil
	withTail := failing
	withTail.CI.Checks[0].LogTail = "tail"
	withTail.CI.FailedChecks[0].LogTail = "tail"
	withTail.CI.FailureLogTail = "tail"
	local.CIHash = ciSemanticHash(withTail.CI)
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = []domain.PullRequestCheck{{Name: "build", Status: domain.PRCheckFailed, LogTail: "tail"}}
	pollOnce(t, obs)
	if provider.logCalls != 0 {
		t.Fatalf("unchanged fingerprint fetched logs again: %d", provider.logCalls)
	}
	if len(store.writes) != 0 {
		t.Fatalf("unchanged failed fingerprint with stored log tail should not write, got %d writes", len(store.writes))
	}
}

func TestEnrichFailureLogsDoesNotRefetchExistingTailOrMissingProviderID(t *testing.T) {
	obsValue := testObs(1)
	obsValue.CI.Summary = string(domain.CIFailing)
	obsValue.CI.FailedFingerprint = "fp"
	obsValue.CI.Checks = []ports.SCMCheckObservation{
		{Name: "build", Status: string(domain.PRCheckFailed), Conclusion: "failure", ProviderID: "99", LogTail: "provider supplied tail"},
		{Name: "lint", Status: string(domain.PRCheckFailed), Conclusion: "failure"},
	}
	obsValue.CI.FailedChecks = append([]ports.SCMCheckObservation(nil), obsValue.CI.Checks...)

	provider := &fakeProvider{logTails: map[string]string{"build": "fetched tail", "lint": "should not fetch"}}
	obs := newTestObserver(testStoreWithSession(), provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.enrichFailureLogs(context.Background(), &obsValue, domain.PullRequest{})

	if provider.logCalls != 0 {
		t.Fatalf("log calls = %d, want 0 when tail already exists or provider id is missing", provider.logCalls)
	}
	if got := obsValue.CI.FailedChecks[0].LogTail; got != "provider supplied tail" {
		t.Fatalf("existing tail changed: got %q", got)
	}
	if got := obsValue.CI.FailedChecks[1].LogTail; got != "" {
		t.Fatalf("tail without provider id = %q, want empty", got)
	}
	if got := obsValue.CI.FailureLogTail; got != "provider supplied tail" {
		t.Fatalf("FailureLogTail = %q, want only existing tail", got)
	}
}

func TestPoll_ReviewPollingRespectsInterval(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix"}}}}}}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(120, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(90, 0).UTC()
	pollOnce(t, obs)
	if provider.reviewCalls != 0 {
		t.Fatalf("review fetched before interval: %d", provider.reviewCalls)
	}
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(0, 0).UTC()
	pollOnce(t, obs)
	if provider.reviewCalls != 1 {
		t.Fatalf("review not fetched after interval: %d", provider.reviewCalls)
	}
	if len(store.writes) == 0 || store.writes[0].reviewMode != ports.ReviewWriteReplace {
		t.Fatalf("review refresh not persisted with replace mode: %#v", store.writes)
	}
}

func TestPoll_UnchangedHashesDoNotWriteOrNotify(t *testing.T) {
	store := testStoreWithSession()
	obsValue := testObs(1)
	local := knownPR(1)
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	local.ReviewHash = reviewSemanticHash(obsValue.Review)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	pollOnce(t, obs)
	if len(store.writes) != 0 || len(lc.observed) != 0 {
		t.Fatalf("unchanged hashes wrote/notified: writes=%d observed=%d", len(store.writes), len(lc.observed))
	}
}

func TestPoll_ReviewHashDrivesPersistenceAndLifecycle(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Reviews:  []ports.SCMReviewSummaryObservation{{ID: "review-1", Author: "ann", State: string(domain.ReviewChangesRequest), URL: "https://github.com/o/r/pull/1#pullrequestreview-1", SubmittedAt: time.Unix(199, 0).UTC()}},
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, IsBot: true, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", IsBot: false, Body: "fix this"}}}},
	}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	pollOnce(t, obs)
	if len(store.writes) == 0 || len(store.writes[0].comments) != 1 {
		t.Fatalf("review change not persisted: %#v", store.writes)
	}
	if len(store.writes[0].reviews) != 1 || store.writes[0].reviews[0].URL != "https://github.com/o/r/pull/1#pullrequestreview-1" {
		t.Fatalf("review summaries not persisted: %#v", store.writes[0].reviews)
	}
	if len(store.writes[0].comments) != 1 || store.writes[0].comments[0].IsBot {
		t.Fatalf("comment author identity was not preserved through persistence: %#v", store.writes[0].comments)
	}
	if len(store.writes) != 2 {
		t.Fatalf("review change with lifecycle should write held-back facts then acknowledgement, got %d writes", len(store.writes))
	}
	if store.writes[0].reviewMode != ports.ReviewWriteReplace {
		t.Fatalf("initial review write mode = %v, want replace", store.writes[0].reviewMode)
	}
	if store.writes[1].reviewMode != ports.ReviewWritePreserve || len(store.writes[1].comments) != 0 {
		t.Fatalf("lifecycle acknowledgement should preserve review rows, got mode=%v comments=%d", store.writes[1].reviewMode, len(store.writes[1].comments))
	}
	if len(lc.observed) != 1 || !lc.observed[0].Changed.Review {
		t.Fatalf("review change not notified: %#v", lc.observed)
	}
}

func TestPoll_PartialReviewRefreshUsesMergeMode(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Partial:  true,
		Threads:  []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", Body: "fix this"}}}},
	}
	provider := reviewOnlyProvider(review)
	obs := newTestObserver(store, provider, nil, time.Unix(210, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	pollOnce(t, obs)
	if len(store.writes) != 1 {
		t.Fatalf("writes = %#v, want one partial review merge", store.writes)
	}
	if store.writes[0].reviewMode != ports.ReviewWriteMerge {
		t.Fatalf("review mode = %v, want merge", store.writes[0].reviewMode)
	}
	if store.writes[0].pr.ReviewHash != reviewSemanticHash(review) {
		t.Fatalf("review hash = %q, want partial hash %q", store.writes[0].pr.ReviewHash, reviewSemanticHash(review))
	}
}

func TestPoll_PartialCIPreservesDurableChecks(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.CI = domain.CIPassing
	local.ObservedAt = time.Unix(10, 0).UTC()
	local.CIObservedAt = time.Unix(11, 0).UTC()
	durableChecks := []domain.PullRequestCheck{
		{Name: "build", CommitHash: "sha1", Status: domain.PRCheckPassed, Conclusion: "success", URL: "ci"},
		{Name: "lint", CommitHash: "sha1", Status: domain.PRCheckPassed, Conclusion: "success", URL: "ci-lint"},
	}
	store.checks[local.URL] = durableChecks
	// Compute the durable CI hash from the full two-check snapshot so the
	// incoming one-check partial observation differs and would trigger a
	// CI change write without the gate.
	durableObs := testObs(1)
	durableObs.CI = ports.SCMCIObservation{
		Summary: string(domain.CIPassing),
		HeadSHA: "sha1",
		Checks: []ports.SCMCheckObservation{
			{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"},
			{Name: "lint", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci-lint"},
		},
	}
	local.CIHash = ciSemanticHash(durableObs.CI)
	local.MetadataHash = metadataSemanticHash(durableObs)
	store.prs["p-1"] = []domain.PullRequest{local}
	// Provider returns a truncated CI snapshot: Partial=true, only one of
	// the two durable checks is present. Without the CI.Partial gate this
	// capped snapshot would be persisted as Fetched=true complete and
	// overwrite the durable lint check. The title differs so a metadata
	// change forces a write even when the CI hash is preserved by the gate.
	partialObs := testObs(1)
	partialObs.PR.Title = "PR (updated)"
	partialObs.CI = ports.SCMCIObservation{
		Summary: string(domain.CIPassing),
		HeadSHA: "sha1",
		Partial: true,
		Checks:  []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}},
	}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): partialObs},
	}
	obs := newTestObserver(store, provider, nil, time.Unix(210, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	pollOnce(t, obs)
	if len(store.writes) != 1 {
		t.Fatalf("writes = %#v, want one write", store.writes)
	}
	write := store.writes[0]
	// The durable CI hash must be preserved — the capped snapshot must
	// not advance it as if it were a complete observation.
	if write.pr.CIHash != local.CIHash {
		t.Fatalf("CI hash = %q, want preserved durable %q", write.pr.CIHash, local.CIHash)
	}
	// CIObservedAt must not advance — the partial snapshot is not an
	// authoritative complete observation.
	if !write.pr.CIObservedAt.Equal(local.CIObservedAt) {
		t.Fatalf("CIObservedAt = %s, want preserved durable %s", write.pr.CIObservedAt, local.CIObservedAt)
	}
}

func TestPoll_ReviewOnlyRefreshPreservesLocalCIAndMetadata(t *testing.T) {
	store := testStoreWithSession()
	localObs := testObs(1)
	local := knownPR(1)
	local.CI = domain.CIPassing
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	local.MetadataHash = metadataSemanticHash(localObs)
	local.CIHash = ciSemanticHash(localObs.CI)
	local.ObservedAt = time.Unix(10, 0).UTC()
	local.CIObservedAt = time.Unix(11, 0).UTC()
	local.ReviewObservedAt = time.Unix(12, 0).UTC()
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = []domain.PullRequestCheck{
		{Name: "build", CommitHash: "sha1", Status: domain.PRCheckPassed, Conclusion: "success", URL: "ci"},
		{Name: "stale", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "old-ci", LogTail: "old tail"},
	}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := reviewOnlyProvider(review)
	now := time.Unix(200, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	pollOnce(t, obs)
	if len(store.writes) == 0 {
		t.Fatalf("writes = %d, want review-only write", len(store.writes))
	}
	write := store.writes[len(store.writes)-1]
	if write.pr.MetadataHash != local.MetadataHash {
		t.Fatalf("metadata hash changed on review-only refresh: got %q want %q", write.pr.MetadataHash, local.MetadataHash)
	}
	if write.pr.CIHash != local.CIHash {
		t.Fatalf("CI hash changed on review-only refresh: got %q want %q", write.pr.CIHash, local.CIHash)
	}
	if !write.pr.ObservedAt.Equal(local.ObservedAt) {
		t.Fatalf("ObservedAt changed on review-only refresh: got %s want %s", write.pr.ObservedAt, local.ObservedAt)
	}
	if !write.pr.CIObservedAt.Equal(local.CIObservedAt) {
		t.Fatalf("CIObservedAt changed on review-only refresh: got %s want %s", write.pr.CIObservedAt, local.CIObservedAt)
	}
	if !write.pr.ReviewObservedAt.Equal(now) {
		t.Fatalf("ReviewObservedAt = %s, want %s", write.pr.ReviewObservedAt, now)
	}
	if len(write.checks) != 1 || write.checks[0].Name != "build" {
		t.Fatalf("review-only local reconstruction should include current-head checks only: %#v", write.checks)
	}
}

func TestPoll_ReviewFetchFailureDoesNotUpdateReviewDecision(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = reviewSemanticHash(ports.SCMReviewObservation{Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "old", Comments: []ports.SCMReviewCommentObservation{{ID: "c-old", Body: "old"}}}}})
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewApproved)
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue},
		reviewErr:    errors.New("review API down"),
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(300, 0).UTC())
	pollOnce(t, obs)
	if provider.reviewCalls != 1 {
		t.Fatalf("reviewCalls = %d, want 1", provider.reviewCalls)
	}
	if len(store.writes) != 0 || len(lc.observed) != 0 {
		t.Fatalf("review fetch failure should not persist/notify stale review data: writes=%#v lifecycle=%#v", store.writes, lc.observed)
	}
	if !obs.Cache.ReviewRefreshFailed[prKey(testRepo, 1)] {
		t.Fatalf("review fetch failure was not marked for retry")
	}
}

func TestPoll_SuccessfulReviewRefreshClearsRetryCacheSlot(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads:  []ports.SCMReviewThreadObservation{thread1("", "fix")},
	}
	provider := reviewOnlyProvider(review)
	obs := newTestObserver(store, provider, nil, time.Unix(350, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.cacheSetBool(obs.Cache.ReviewRefreshFailed, &obs.Cache.reviewFailedOrder, prKey(testRepo, 1), true)

	pollOnce(t, obs)
	if _, ok := obs.Cache.ReviewRefreshFailed[prKey(testRepo, 1)]; ok {
		t.Fatalf("successful review refresh should delete retry map entry, got %#v", obs.Cache.ReviewRefreshFailed)
	}
	for _, key := range obs.Cache.reviewFailedOrder {
		if key == prKey(testRepo, 1) {
			t.Fatalf("successful review refresh should remove retry order slot, got %#v", obs.Cache.reviewFailedOrder)
		}
	}
}

func TestPoll_ReviewObservedAtZeroTriggersReviewRefresh(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewApproved
	local.ReviewHash = "some-hash"       // non-empty but review_observed_at is zero
	local.ReviewObservedAt = time.Time{} // review threads were never fetched
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewApproved),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := reviewOnlyProvider(review)
	now := time.Unix(400, 0).UTC()
	obs := newTestObserver(store, provider, nil, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	pollOnce(t, obs)
	if provider.reviewCalls != 1 {
		t.Fatalf("reviewCalls = %d, want 1 (should trigger FetchReviewThreads when review_observed_at is zero)", provider.reviewCalls)
	}
	write := lastWrite(t, store)
	if !write.pr.ReviewObservedAt.Equal(now) {
		t.Fatalf("ReviewObservedAt = %s, want %s", write.pr.ReviewObservedAt, now)
	}
	requireThread1Persisted(t, write)
}

func TestNeedsReviewRefresh_UpdatedAtProviderTriggersRefresh(t *testing.T) {
	store := testStoreWithSession()
	obs := newTestObserver(store, &fakeProvider{}, nil, time.Unix(500, 0).UTC())
	key := prKey(testRepo, 1)

	base := domain.PullRequest{
		Number:           1,
		Review:           domain.ReviewApproved,
		ReviewHash:       "hash",
		ReviewObservedAt: time.Unix(100, 0).UTC(),
	}

	// updated_at newer than ReviewObservedAt → refresh
	if !obs.needsReviewRefresh(key, base, string(domain.ReviewApproved), true, time.Unix(200, 0).UTC(), time.Unix(500, 0).UTC()) {
		t.Fatal("expected refresh when updatedAtProvider is newer than ReviewObservedAt")
	}

	// updated_at older than ReviewObservedAt → no refresh
	if obs.needsReviewRefresh(key, base, string(domain.ReviewApproved), true, time.Unix(50, 0).UTC(), time.Unix(500, 0).UTC()) {
		t.Fatal("expected no refresh when updatedAtProvider is older than ReviewObservedAt")
	}

	// updated_at equal to ReviewObservedAt → no refresh
	if obs.needsReviewRefresh(key, base, string(domain.ReviewApproved), true, time.Unix(100, 0).UTC(), time.Unix(500, 0).UTC()) {
		t.Fatal("expected no refresh when updatedAtProvider equals ReviewObservedAt")
	}

	// updated_at is zero → no refresh (falls back to existing conditions)
	if obs.needsReviewRefresh(key, base, string(domain.ReviewApproved), true, time.Time{}, time.Unix(500, 0).UTC()) {
		t.Fatal("expected no refresh when updatedAtProvider is zero")
	}

	// hasObs is false → updated_at check skipped, no refresh
	if obs.needsReviewRefresh(key, base, string(domain.ReviewApproved), false, time.Unix(200, 0).UTC(), time.Unix(500, 0).UTC()) {
		t.Fatal("expected no refresh when hasObs is false even with newer updatedAtProvider")
	}
}

func TestPoll_UpdatedAtProviderTriggersReviewRefresh(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewApproved
	local.ReviewHash = "review-hash"
	local.ReviewObservedAt = time.Unix(100, 0).UTC()
	store.prs["p-1"] = []domain.PullRequest{local}

	obsValue := testObs(1)
	obsValue.PR.UpdatedAtProvider = time.Unix(200, 0).UTC()
	obsValue.Review = ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)}

	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewApproved),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "comment")},
	}
	provider := refreshProvider(obsValue, review)
	now := time.Unix(500, 0).UTC()
	obs := newTestObserver(store, provider, nil, now)
	pollOnce(t, obs)
	if provider.reviewCalls != 1 {
		t.Fatalf("reviewCalls = %d, want 1 (should trigger FetchReviewThreads when updatedAtProvider is newer)", provider.reviewCalls)
	}
	write := lastWrite(t, store)
	if !write.pr.ReviewObservedAt.Equal(now) {
		t.Fatalf("ReviewObservedAt = %s, want %s", write.pr.ReviewObservedAt, now)
	}
	requireThread1Persisted(t, write)
}

func TestPoll_UpdatedAtProviderStaleDoesNotTriggerReviewRefresh(t *testing.T) {
	store := testStoreWithSession()
	obsValue := testObs(1)
	obsValue.PR.UpdatedAtProvider = time.Unix(100, 0).UTC() // older than ReviewObservedAt
	obsValue.Review = ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)}

	local := knownPR(1)
	local.Review = domain.ReviewApproved
	local.ReviewObservedAt = time.Unix(200, 0).UTC()
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	local.ReviewHash = reviewSemanticHash(obsValue.Review)
	store.prs["p-1"] = []domain.PullRequest{local}

	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue},
		reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewApproved)}},
	}
	obs := newTestObserver(store, provider, nil, time.Unix(500, 0).UTC())
	pollOnce(t, obs)
	if provider.reviewCalls != 0 {
		t.Fatalf("reviewCalls = %d, want 0 (stale updatedAtProvider should not trigger refresh)", provider.reviewCalls)
	}
	if len(store.writes) != 0 {
		t.Fatalf("expected no writes when stale, got %d", len(store.writes))
	}
}

func TestPoll_RefreshReviewsDoesNotDowngradeChangesRequested(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// The fast-path observation set ReviewChangesRequest (e.g. from
	// detailed_merge_status=requested_changes). FetchReviewThreads derives the
	// decision from approvals and returns a lower-priority "approved".
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewChangesRequest)
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewApproved),
		Partial:  true,
		Reviews:  []ports.SCMReviewSummaryObservation{{ID: "review-1", Author: "ann", State: string(domain.ReviewApproved), URL: "https://github.com/o/r/pull/1#pullrequestreview-1", SubmittedAt: time.Unix(199, 0).UTC()}},
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := refreshProvider(obsValue, review)
	obs := newTestObserver(store, provider, nil, time.Unix(500, 0).UTC())
	pollOnce(t, obs)
	if provider.reviewCalls != 1 {
		t.Fatalf("reviewCalls = %d, want 1", provider.reviewCalls)
	}
	write := lastWrite(t, store)
	if write.pr.Review != domain.ReviewChangesRequest {
		t.Fatalf("Review = %q, want %q (should not downgrade ChangesRequested to lower-priority decision)", write.pr.Review, domain.ReviewChangesRequest)
	}
	// Threads, summaries, and partial flag from FetchReviewThreads must still
	// be adopted regardless of the decision guard.
	requireThread1Persisted(t, write)
	if len(write.reviews) != 1 || write.reviews[0].ID != "review-1" {
		t.Fatalf("expected review summary review-1 persisted, got %#v", write.reviews)
	}
	if write.reviewMode != ports.ReviewWriteMerge {
		t.Fatalf("expected ReviewWriteMerge mode when Partial is true, got %v", write.reviewMode)
	}
}

func TestPoll_RefreshReviewsOverwritesWithEqualOrHigherPriority(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewApproved
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// Local decision is ReviewApproved (priority 1). FetchReviewThreads
	// returns ReviewChangesRequest (priority 3) — higher priority, should
	// overwrite as before (no regression).
	local.ReviewObservedAt = time.Unix(100, 0).UTC()
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewApproved)
	obsValue.PR.UpdatedAtProvider = time.Unix(200, 0).UTC()
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewChangesRequest),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := refreshProvider(obsValue, review)
	obs := newTestObserver(store, provider, nil, time.Unix(500, 0).UTC())
	pollOnce(t, obs)
	write := lastWrite(t, store)
	if write.pr.Review != domain.ReviewChangesRequest {
		t.Fatalf("Review = %q, want %q (higher-priority decision should overwrite)", write.pr.Review, domain.ReviewChangesRequest)
	}
	requireThread1Persisted(t, write)
}

func TestPoll_RefreshReviewsDowngradeToNoneBlocked(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// FetchReviewThreads returns Decision="none" (priority 0), which is
	// lower than ReviewChangesRequest (priority 3). The guard must keep the
	// existing ChangesRequested decision but still adopt the threads.
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewChangesRequest)
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewNone),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := refreshProvider(obsValue, review)
	obs := newTestObserver(store, provider, nil, time.Unix(500, 0).UTC())
	pollOnce(t, obs)
	write := lastWrite(t, store)
	if write.pr.Review != domain.ReviewChangesRequest {
		t.Fatalf("Review = %q, want %q (should not downgrade to none)", write.pr.Review, domain.ReviewChangesRequest)
	}
	requireThread1Persisted(t, write)
}

func TestPoll_RefreshReviewsDowngradeToReviewRequiredBlocked(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// FetchReviewThreads returns Decision="review_required" (priority 2),
	// which is lower than ReviewChangesRequest (priority 3). The guard must
	// keep the existing ChangesRequested decision but still adopt the threads.
	obsValue := testObs(1)
	obsValue.Review.Decision = string(domain.ReviewChangesRequest)
	review := ports.SCMReviewObservation{
		Decision: string(domain.ReviewRequired),
		Threads:  []ports.SCMReviewThreadObservation{thread1("ann", "fix")},
	}
	provider := refreshProvider(obsValue, review)
	obs := newTestObserver(store, provider, nil, time.Unix(500, 0).UTC())
	pollOnce(t, obs)
	write := lastWrite(t, store)
	if write.pr.Review != domain.ReviewChangesRequest {
		t.Fatalf("Review = %q, want %q (should not downgrade to review_required)", write.pr.Review, domain.ReviewChangesRequest)
	}
	requireThread1Persisted(t, write)
}

func TestPoll_CompletenessUpgradePersistsWithUnchangedReviewContent(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	// Simulate a row upgraded by the conservative review_partial default: the
	// stored flag says partial even though the review hash was computed from a
	// full observation. A later full fetch with identical content must still
	// persist the completeness repair — the content-hash match must not skip it.
	local.ReviewPartial = true
	store.prs["p-1"] = []domain.PullRequest{local}
	obsValue := testObs(1)
	review := ports.SCMReviewObservation{Decision: string(domain.ReviewNone)}
	provider := refreshProvider(obsValue, review)
	obs := newTestObserver(store, provider, nil, time.Unix(300, 0).UTC())
	// Force a review retry without touching any content: the retry cache slot
	// is what a failed-fetch recovery or a post-upgrade poll would hit.
	obs.cacheSetBool(obs.Cache.ReviewRefreshFailed, &obs.Cache.reviewFailedOrder, prKey(testRepo, 1), true)
	pollOnce(t, obs)
	if len(store.writes) != 1 {
		t.Fatalf("completeness repair was skipped on unchanged content: writes=%d", len(store.writes))
	}
	written := store.writes[0].pr
	if written.ReviewPartial {
		t.Fatalf("full fetch did not clear the partial flag: %+v", written)
	}
	if written.ReviewObservedAt.IsZero() {
		t.Fatalf("successful review fetch left no review observation: %+v", written)
	}
	if store.writes[0].reviewMode != ports.ReviewWriteReplace {
		t.Fatalf("review mode = %v, want replace", store.writes[0].reviewMode)
	}
}
