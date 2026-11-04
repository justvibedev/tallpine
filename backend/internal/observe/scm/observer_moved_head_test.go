package scm

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestPoll_MovedHeadRefreshedBehindNotModifiedRepoGuard reproduces B472: after
// a rebase push the provider reports a new head, but the repo-list guard
// answers 304 (a sync cursor exists) and the old head's commit-check guard is
// unchanged. The open PR must still be re-fetched once its snapshot is older
// than DefaultPRMaxAge, so the stored head_sha cannot stay stale forever.
func TestPoll_MovedHeadRefreshedBehindNotModifiedRepoGuard(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Merged, local.Closed = false, false
	store.prs["p-1"] = []domain.PullRequest{local}

	moved := testObs(1)
	moved.PR.HeadSHA = "sha2-after-rebase"

	repoKey := prKey(testRepo, 0)
	commitCacheKey := commitKey(testRepo, "sha1")
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{repoKey: {ETag: "v1", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitCacheKey: {ETag: "ci1", NotModified: true}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): moved},
	}
	now := time.Unix(100000, 0).UTC()
	obs := newTestObserver(store, provider, &fakeLifecycle{}, now)
	obs.Cache.RepoPRListETag[repoKey] = "v1"
	obs.Cache.CommitChecksETag[commitCacheKey] = "ci1"
	obs.Cache.LastSyncCursor[repoKey] = now.Add(-time.Hour)
	obs.Cache.LastPRFetchAt[prKey(testRepo, 1)] = now.Add(-(DefaultPRMaxAge + time.Minute))

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want 1 forced refresh of the moved-head PR", len(provider.fetchBatches))
	}
	var got string
	for _, w := range store.writes {
		if w.pr.Number == 1 {
			got = w.pr.HeadSHA
		}
	}
	if got != "sha2-after-rebase" {
		t.Fatalf("stored head = %q, want sha2-after-rebase", got)
	}
}
