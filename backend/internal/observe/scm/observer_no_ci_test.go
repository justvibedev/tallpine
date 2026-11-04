package scm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/pkg/contract"
)

// TestMergeabilityFromProviderFacts_UnstableOutranksBlockers pins issue #2401: an
// UNSTABLE PR is mergeable-with-a-failing-optional-check, so it must NOT be
// downgraded to blocked by the ci/review/draft blockers. doc.go rule (3) beats
// rules (5) and (6), matching the mergeabilityFromGraphQL sibling.
func TestMergeabilityFromProviderFacts_UnstableOutranksBlockers(t *testing.T) {
	cases := []struct {
		name         string
		mergeable    string
		state        string
		ci           string
		review       string
		draft        bool
		want         domain.Mergeability
		wantBlockers []string
		wantBehind   bool
	}{
		{
			// The repro from the issue: optional codecov fails, rollup is
			// FAILURE (-> ci failing), review approved, GitHub still merges.
			name:      "unstable with failing ci stays unstable",
			mergeable: "MERGEABLE", state: "UNSTABLE",
			ci:     string(domain.CIFailing),
			review: string(domain.ReviewApproved),
			want:   domain.MergeUnstable,
		},
		{
			name:      "unstable with changes requested stays unstable",
			mergeable: "MERGEABLE", state: "UNSTABLE",
			ci:     string(domain.CIPassing),
			review: string(domain.ReviewChangesRequest),
			want:   domain.MergeUnstable,
		},
		{
			name:      "unstable behind base keeps the behind_base signal",
			mergeable: "MERGEABLE", state: "BEHIND_BASE",
			ci:         string(domain.CIFailing),
			want:       domain.MergeBlocked,
			wantBehind: true, wantBlockers: []string{"behind_base", "ci_failing"},
		},
		// Regressions guarded: the non-UNSTABLE paths must be untouched.
		{
			name:      "conflicting still wins",
			mergeable: "CONFLICTING", state: "DIRTY",
			want: domain.MergeConflicting, wantBlockers: []string{"conflicts"},
		},
		{
			name:      "blocked by provider still collects every blocker",
			mergeable: "MERGEABLE", state: "BLOCKED",
			ci: string(domain.CIFailing), review: string(domain.ReviewChangesRequest), draft: true,
			want:         domain.MergeBlocked,
			wantBlockers: []string{"blocked_by_provider", "draft", "ci_failing", "changes_requested"},
		},
		{
			name:      "clean + failing ci still blocks",
			mergeable: "MERGEABLE", state: "CLEAN",
			ci:   string(domain.CIFailing),
			want: domain.MergeBlocked, wantBlockers: []string{"ci_failing"},
		},
		{
			name:      "clean + approved is mergeable",
			mergeable: "MERGEABLE", state: "CLEAN",
			ci: string(domain.CIPassing), review: string(domain.ReviewApproved),
			want: domain.MergeMergeable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeabilityFromProviderFacts(tc.mergeable, tc.state, tc.ci, tc.review, tc.draft)
			if got.State != string(tc.want) {
				t.Fatalf("State = %q, want %q", got.State, tc.want)
			}
			if got.Mergeable != (tc.want == domain.MergeMergeable) {
				t.Fatalf("Mergeable = %v for state %q", got.Mergeable, got.State)
			}
			if got.Conflict != (tc.want == domain.MergeConflicting) {
				t.Fatalf("Conflict = %v for state %q", got.Conflict, got.State)
			}
			if got.BehindBase != tc.wantBehind {
				t.Fatalf("BehindBase = %v, want %v", got.BehindBase, tc.wantBehind)
			}
			if tc.wantBlockers == nil {
				if len(got.Blockers) != 0 {
					t.Fatalf("Blockers = %v, want none", got.Blockers)
				}
				return
			}
			if strings.Join(got.Blockers, ",") != strings.Join(tc.wantBlockers, ",") {
				t.Fatalf("Blockers = %v, want %v", got.Blockers, tc.wantBlockers)
			}
		})
	}
}

func TestPoll_NoCIProjectNeedsTallpineApprovalOfHeadForPassing(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	approved := func(sha string) fakeReviewRuns {
		return fakeReviewRuns{{ID: "r1", SessionID: "p-1", Harness: "claude-code", PRURL: noChecks.PR.URL, TargetSHA: sha, Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved}}
	}
	for _, tt := range []struct {
		name     string
		noCI     bool
		runs     fakeReviewRuns
		decision domain.ReviewDecision
		want     domain.CIState
	}{
		{"opted in, Tallpine approved head", true, approved("sha1"), domain.ReviewNone, domain.CIPassing},
		{"opted in, provider APPROVED (possibly stale) without Tallpine approval", true, nil, domain.ReviewApproved, domain.CIUnknown},
		{"opted in, unreviewed", true, nil, domain.ReviewNone, domain.CIUnknown},
		{"opted in, Tallpine approved an older head", true, approved("sha0"), domain.ReviewNone, domain.CIUnknown},
		{"default", false, approved("sha1"), domain.ReviewApproved, domain.CIUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := testStoreWithSession()
			p := store.projects["p"]
			p.Config.NoCI = tt.noCI
			store.projects["p"] = p
			local := knownPR(1)
			local.MetadataHash = "" // force a refresh
			store.prs["p-1"] = []domain.PullRequest{local}
			provider := &fakeProvider{
				repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}},
				observations: map[string]ports.SCMObservation{prKey(testRepo, 1): func() ports.SCMObservation {
					o := noChecks
					o.Review.Decision = string(tt.decision)
					return o
				}()},
			}
			lc := &fakeLifecycle{}
			obs := newTestObserver(store, provider, lc, time.Unix(2, 0).UTC())
			obs.reviewRuns = tt.runs
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(lc.observed) != 1 || domain.CIState(lc.observed[0].CI.Summary) != tt.want {
				t.Fatalf("lifecycle observations = %#v, want one with CI %q", lc.observed, tt.want)
			}
			if len(store.writes) == 0 || store.writes[len(store.writes)-1].pr.CI != tt.want {
				t.Fatalf("persisted writes = %#v, want CI %q", store.writes, tt.want)
			}
			// The persisted row is what lifecycle, notification reconciliation
			// and the UI judge: an unapproved noCI PR must never read as ready.
			ready := domain.MergeReadinessOf(store.writes[len(store.writes)-1].pr, false).ReadyToMerge()
			if ready != (tt.want == domain.CIPassing) {
				t.Fatalf("ready to merge = %v with CI %q", ready, tt.want)
			}
		})
	}
}

func TestPoll_NoCIRefetchesWhenApprovalArrivesAfterUnknownWasStored(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	local := knownPR(1)
	local.CI = domain.CIUnknown
	local.CIHash = ciSemanticHash(noChecks.CI)
	runs := fakeReviewRuns{{ID: "r1", SessionID: "p-1", Harness: "claude-code", PRURL: local.URL, TargetSHA: "sha1", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved}}
	obs, store, provider := quietNoCIPoll(t, true, local, nil, noChecks, runs)
	// The verdict landed after the poll that stored "unknown", in a quiet repo
	// with an incremental cursor: listing and checks are 304 and the snapshot is fresh,
	// so the max-age path does not fire, so only the noCI staleness rule can pick it up.
	now := time.Unix(100, 0).UTC()
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Minute)
	obs.Cache.LastPRFetchAt[keyForTrackedPR(local, testRepo)] = now // fresh: isolates the noCI rule from the max-age fallback
	for i := 0; i < 2; i++ {
		if err := obs.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want exactly one refetch for the newly approved head", len(provider.fetchBatches))
	}
	last := store.writes[len(store.writes)-1].pr
	if last.CI != domain.CIPassing || !last.NoCIWaived() {
		t.Fatalf("persisted PR CI = %q hash = %q, want waived passing", last.CI, last.CIHash)
	}
}

func TestPoll_NoCIOptOutRefetchesPolicyDerivedPassing(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	waived := noChecks
	waived.CI.Summary = string(domain.CIPassing)
	waived.CI.NoCIWaived = true
	local := knownPR(1)
	local.CI = domain.CIPassing
	local.CIHash = ciSemanticHash(waived.CI) // stored while noCI was on
	obs, store, provider := quietNoCIPoll(t, false, local, nil, noChecks, nil)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want a refetch after noCI was turned off", len(provider.fetchBatches))
	}
	if len(store.writes) == 0 || store.writes[len(store.writes)-1].pr.CI != domain.CIUnknown {
		t.Fatalf("persisted writes = %#v, want CI unknown", store.writes)
	}
}

// The reviewer's scenario: an incremental sync cursor exists and the repo
// listing is a 304 and the snapshot is fresh, so the max-age path does not
// fire. Only the noCI staleness rule can refresh a
// waived "passing" after noCI is turned off; a provider-passing PR in the same
// quiet poll is not fetched.
func TestPoll_NoCIOptOutRefetchesUnderIncrementalCursor(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	waived := noChecks
	waived.CI.Summary = string(domain.CIPassing)
	waived.CI.NoCIWaived = true
	for _, tt := range []struct {
		name        string
		ciHash      string
		wantFetches int
	}{
		{"waived passing after noCI turned off", ciSemanticHash(waived.CI), 1},
		{"provider passing (control)", knownPR(1).CIHash, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			local := knownPR(1)
			local.CI = domain.CIPassing
			local.CIHash = tt.ciHash
			obs, store, provider := quietNoCIPoll(t, false, local, nil, noChecks, nil)
			now := time.Unix(100, 0).UTC()
			obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Minute)
			obs.Cache.LastPRFetchAt[keyForTrackedPR(local, testRepo)] = now // fresh: isolates the noCI rule from the max-age fallback
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(provider.fetchBatches) != tt.wantFetches {
				t.Fatalf("fetch batches = %d, want %d", len(provider.fetchBatches), tt.wantFetches)
			}
			if tt.wantFetches == 1 && (len(store.writes) == 0 || store.writes[len(store.writes)-1].pr.CI != domain.CIUnknown) {
				t.Fatalf("persisted writes = %#v, want CI unknown after opt-out", store.writes)
			}
		})
	}
}

func TestPoll_NoCIRecheckIsRetriedAfterFailedRefetchAndPruned(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	local := knownPR(1)
	local.CI = domain.CIUnknown
	local.CIHash = ciSemanticHash(noChecks.CI)
	runs := fakeReviewRuns{{ID: "r1", SessionID: "p-1", Harness: "claude-code", PRURL: local.URL, TargetSHA: "sha1", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved}}
	obs, store, provider := quietNoCIPoll(t, true, local, nil, noChecks, runs)
	key := keyForTrackedPR(local, testRepo)

	// A failed (e.g. rate-limited) refetch must not consume the recheck.
	provider.fetchErr = errors.New("rate limited")
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, recorded := obs.noCIRecheck[key]; recorded {
		t.Fatal("recheck token recorded although the refetch failed")
	}
	provider.fetchErr = nil
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 2 {
		t.Fatalf("fetch batches = %d, want the failed refetch retried", len(provider.fetchBatches))
	}
	if _, recorded := obs.noCIRecheck[key]; !recorded {
		t.Fatal("recheck token not recorded after the successful refetch")
	}
	if last := store.writes[len(store.writes)-1].pr; last.CI != domain.CIPassing {
		t.Fatalf("persisted CI = %q, want passing", last.CI)
	}

	// Once the PR is no longer tracked its token is pruned.
	store.prs["p-1"] = nil
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(obs.noCIRecheck) != 0 {
		t.Fatalf("noCIRecheck = %v, want pruned", obs.noCIRecheck)
	}
}

// Session status and board placement read the stored PR's mergeability, not
// merge readiness. With noCI on, AutoReview off and no Tallpine approval of the
// current head, a clean MERGEABLE no-checks PR must not read Mergeable/Ready.
func TestPoll_NoCIUnapprovedPRIsNotMergeableOnStatusOrBoard(t *testing.T) {
	noChecks := testObs(1) // provider MERGEABLE, review decision none
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	approved := fakeReviewRuns{{ID: "r1", SessionID: "p-1", Harness: "claude-code", PRURL: noChecks.PR.URL, TargetSHA: "sha1", Status: domain.ReviewRunDelivered, Verdict: domain.VerdictApproved}}
	for _, tt := range []struct {
		name          string
		runs          fakeReviewRuns
		wantMergeable bool
	}{
		{"no current-head approval", nil, false},
		{"Tallpine approved the current head", approved, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := testStoreWithSession()
			p := store.projects["p"]
			p.Config.NoCI = true
			store.projects["p"] = p
			local := knownPR(1)
			local.MetadataHash = "" // force a refresh
			store.prs["p-1"] = []domain.PullRequest{local}
			provider := &fakeProvider{
				repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}},
				observations: map[string]ports.SCMObservation{prKey(testRepo, 1): noChecks},
			}
			obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(2, 0).UTC())
			obs.reviewRuns = tt.runs
			if err := obs.Poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			row := store.writes[len(store.writes)-1].pr
			if row.NoCIAwaitingApproval() == tt.wantMergeable {
				t.Fatalf("NoCIAwaitingApproval = %v, want %v", row.NoCIAwaitingApproval(), !tt.wantMergeable)
			}

			status := contract.DeriveSCMStatus([]contract.PRFacts{{URL: row.URL, CI: row.CI, Review: row.Review, Mergeability: row.Mergeability}})
			if (status == contract.StatusMergeable) != tt.wantMergeable {
				t.Fatalf("session status = %q (mergeability %q), want mergeable=%v", status, row.Mergeability, tt.wantMergeable)
			}
			board := contract.DeriveKanbanPresentation(
				contract.KanbanSessionFacts{SessionFacts: contract.SessionFacts{Activity: contract.ActivityIdle, HasSignal: true}, AutoReview: false},
				[]contract.KanbanPRFacts{{URL: row.URL, CI: row.CI, Review: row.Review, Mergeability: row.Mergeability}},
				time.Unix(2, 0).UTC(), time.Minute,
			)
			ready := board.Column == contract.KanbanReady && board.DisplayStatus == contract.DisplayMergeable
			if ready != tt.wantMergeable {
				t.Fatalf("board = %+v, want Ready/Mergeable=%v", board, tt.wantMergeable)
			}
		})
	}
}

func TestPoll_NoCIOptOutRestoresHeldBackMergeability(t *testing.T) {
	noChecks := testObs(1)
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	held := noChecks.CI
	held.NoCIAwaitingApproval = true
	local := knownPR(1)
	local.CI = domain.CIUnknown
	local.Mergeability = domain.MergeBlocked
	local.CIHash = ciSemanticHash(held) // stored while noCI was on
	obs, store, provider := quietNoCIPoll(t, false, local, nil, noChecks, nil)
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want a refetch after noCI was turned off", len(provider.fetchBatches))
	}
	if row := store.writes[len(store.writes)-1].pr; row.Mergeability != domain.MergeMergeable || row.NoCIAwaitingApproval() {
		t.Fatalf("persisted mergeability = %q (held=%v), want provider mergeable", row.Mergeability, row.NoCIAwaitingApproval())
	}
}

// noCI turned on for an already tracked clean PR stored as unknown/mergeable
// before the gate ever ran on it: in a quiet repo with an incremental cursor
// the next poll must still apply the gate (blocked, +nociwait), and the poll
// after that must not refetch again.
func TestPoll_NoCIActivationAppliesGateToUnmarkedUnknownPR(t *testing.T) {
	noChecks := testObs(1) // provider MERGEABLE
	noChecks.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: "sha1", NoChecks: true}
	local := knownPR(1)
	local.CI = domain.CIUnknown
	local.Mergeability = domain.MergeMergeable
	local.CIHash = ciSemanticHash(noChecks.CI) // stored before noCI: no marker
	obs, store, provider := quietNoCIPoll(t, true, local, nil, noChecks, nil)
	now := time.Unix(100, 0).UTC()
	obs.Cache.LastSyncCursor[prKey(testRepo, 0)] = now.Add(-time.Minute)

	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d, want one refetch after noCI was turned on", len(provider.fetchBatches))
	}
	row := store.writes[len(store.writes)-1].pr
	if row.Mergeability != domain.MergeBlocked || !row.NoCIAwaitingApproval() {
		t.Fatalf("persisted mergeability = %q (held=%v), want blocked with +nociwait", row.Mergeability, row.NoCIAwaitingApproval())
	}

	store.mu.Lock()
	store.prs["p-1"] = []domain.PullRequest{row} // the store now holds the gated row
	store.mu.Unlock()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("fetch batches = %d after the second poll, want no further refetch", len(provider.fetchBatches))
	}
}
