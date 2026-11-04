package scm

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestPoll_PerObservationError_NiledBeforePersistence (Item 7) verifies that
// the observer nils out the Error field before the observation reaches the
// store and lifecycle. The Error field is transient metadata, not durable
// state: the storage layer must never see provider-error classification.
func TestPoll_PerObservationError_NiledBeforePersistence(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.MetadataHash = "durable-meta"
	local.CIHash = "durable-ci"
	local.ReviewHash = "durable-review"
	store.prs["p-1"] = []domain.PullRequest{local}

	// An unchanged observation is not forwarded to lifecycle, so change CI.
	changed := testObs(1)
	changed.CI.Summary = string(domain.CIPassing)
	changed.CI.HeadSHA = "sha1"
	changed.Error = errors.New("transient provider error")
	changed.CI.Checks = []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}}

	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo1", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): changed},
	}
	lc := &fakeLifecycle{}
	now := time.Unix(1000, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The changed observation must reach lifecycle with Error == nil.
	if len(lc.observed) == 0 {
		t.Fatal("no observation reached lifecycle")
	}
	for _, o := range lc.observed {
		if o.Error != nil {
			t.Errorf("lifecycle observed Error = %v, want nil (transient metadata must be nil-ed before persistence)", o.Error)
		}
	}
	for _, w := range store.writes {
		_ = w // store writes carry domain types, not the obs.Error field; the
		// lifecycle assertion above is the authoritative nil-out check.
	}
}
