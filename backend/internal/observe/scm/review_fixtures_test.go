package scm

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/ports"
)

// thread1 is the single review thread (t1 on f.go:2, one comment c1) that most
// review-refresh tests persist and then look for.
func thread1(author, body string) ports.SCMReviewThreadObservation {
	return ports.SCMReviewThreadObservation{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: author, Body: body}}}
}

// reviewOnlyProvider reports the repo PR list as unchanged (304) so only the
// review fetch for PR 1 can run.
func reviewOnlyProvider(review ports.SCMReviewObservation) *fakeProvider {
	return &fakeProvider{
		repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		reviews:    map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
}

// refreshProvider reports a changed repo list, a fast-path observation for PR 1
// and a review fetch result.
func refreshProvider(obsValue ports.SCMObservation, review ports.SCMReviewObservation) *fakeProvider {
	return &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue},
		reviews:      map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review},
	}
}

func pollOnce(t *testing.T, obs *Observer) {
	t.Helper()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// lastWrite returns the most recent store write, failing the test when none.
func lastWrite(t *testing.T, store *fakeStore) fakeWrite {
	t.Helper()
	if len(store.writes) == 0 {
		t.Fatal("expected a write after review refresh")
	}
	return store.writes[len(store.writes)-1]
}

func requireThread1Persisted(t *testing.T, w fakeWrite) {
	t.Helper()
	if len(w.threads) != 1 || w.threads[0].ThreadID != "t1" {
		t.Fatalf("expected review thread t1 persisted, got %#v", w.threads)
	}
}
