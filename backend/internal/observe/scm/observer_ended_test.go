package scm

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// endedStore adds the optional "ended sessions with an open PR" list
// the production sqlite store provides.
type endedStore struct {
	*fakeStore
	ended []domain.SessionRecord
}

func (s *endedStore) ListEndedSessionsWithOpenBacklogPRs(context.Context) ([]domain.SessionRecord, error) {
	return s.ended, nil
}

func endedSessionFixture(t *testing.T) (*endedStore, *fakeProvider, *fakeLifecycle, *Observer) {
	t.Helper()
	store := testStoreWithSession()
	sess := store.sessions[0]
	sess.IsTerminated = true
	store.sessions = nil // a terminated session is not in ListLiveSessions
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	merged := testObs(1)
	merged.PR.Merged = true
	merged.PR.State = string(domain.PRStateMerged)
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo2"}},
		openPRs:      map[string][]ports.SCMPRObservation{}, // the merged PR left the state=open listing
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): merged},
	}
	lc := &fakeLifecycle{}
	es := &endedStore{fakeStore: store, ended: []domain.SessionRecord{sess}}
	now := time.Unix(1300, 0).UTC()
	obs := newTestObserver(store, provider, lc, now)
	obs.store = es
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo1"
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Hour)
	return es, provider, lc, obs
}

// B24: a worker killed right after the merge no longer hides the merge. The
// open PR of an ended session is still polled.
func TestPoll_EndedSessionWithBacklogLinkedOpenPRRecordsTheMerge(t *testing.T) {
	es, _, _, obs := endedSessionFixture(t)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, w := range es.writes {
		if w.pr.Number == 1 && w.pr.Merged {
			return
		}
	}
	t.Fatalf("merge of an ended session's PR not persisted; writes=%#v", es.writes)
}

// An ended session the store does not list (no open PR left) is not polled.
func TestPoll_EndedSessionNotListedByStoreIsNotPolled(t *testing.T) {
	es, provider, _, obs := endedSessionFixture(t)
	es.ended = nil
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(es.writes) != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("polled an ended session: writes=%d batches=%d", len(es.writes), len(provider.fetchBatches))
	}
}

// An ended session only refreshes PRs that are still open: its merged PR is
// not tracked again even when the session opted into merge-driven teardown.
func TestPoll_EndedSessionNeverKeepsTerminalPRsTracked(t *testing.T) {
	es, provider, _, obs := endedSessionFixture(t)
	merged := knownPR(1)
	merged.Merged = true
	es.prs["p-1"] = []domain.PullRequest{merged}
	es.ended[0].TerminateOnPRMerge = true
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(es.writes) != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("re-polled a terminal PR of an ended session: writes=%d batches=%d", len(es.writes), len(provider.fetchBatches))
	}
}

func TestCandidatesForHeadRepoSkipsTrackOnlySessions(t *testing.T) {
	names := map[string]struct{}{"o/r": {}}
	got := candidatesForHeadRepo([]sessionRepo{
		{session: domain.SessionRecord{ID: "live"}, headNames: names},
		{session: domain.SessionRecord{ID: "ended"}, headNames: names, trackOnly: true},
	}, "o/r")
	if len(got) != 1 || got[0].session.ID != "live" {
		t.Fatalf("candidates = %#v, want only the live session", got)
	}
}
