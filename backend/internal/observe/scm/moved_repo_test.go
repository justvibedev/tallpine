package scm

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// B592: a project whose stored origin is on another host (Bitbucket) but whose
// canonical repo URL names the new GitHub home must discover and adopt the
// session's PR from the canonical repository.
func TestPoll_MovedHostProjectScansCanonicalRepo(t *testing.T) {
	store := testStoreWithSession()
	store.projects["p"] = domain.ProjectRecord{ID: "p", RepoOriginURL: "https://bitbucket.org/x/y", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/o/r"}}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", BaseRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("moved-host PR must be attributed to p-1, got %#v", store.writes)
	}
}

// A same-host canonical URL is a fork's upstream: the origin is still scanned.
func TestPoll_SameHostCanonicalKeepsScanningOrigin(t *testing.T) {
	store := testStoreWithSession()
	store.projects["p"] = domain.ProjectRecord{ID: "p", RepoOriginURL: "https://github.com/o/r.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://github.com/up/r"}}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", BaseRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("fork origin PR must still be attributed, got %#v", store.writes)
	}
}

// A canonical URL on a host no provider handles (typo, unsupported host) must
// not stop tracking for a healthy origin.
func TestPoll_UnsupportedCanonicalHostFallsBackToOrigin(t *testing.T) {
	store := testStoreWithSession()
	store.projects["p"] = domain.ProjectRecord{ID: "p", RepoOriginURL: "https://github.com/o/r.git", Config: domain.ProjectConfig{CanonicalRepoURL: "https://bitbucket.org/x/y"}}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		openPRs:      map[string][]ports.SCMPRObservation{prKey(testRepo, 0): {{URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", HeadRepo: "o/r", BaseRepo: "o/r", TargetBranch: "main", HeadSHA: "sha1"}}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) == 0 || store.writes[0].pr.SessionID != "p-1" {
		t.Fatalf("origin PR must still be attributed, got %#v", store.writes)
	}
}
