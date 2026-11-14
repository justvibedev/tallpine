package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestClaimRowsFromSCMSnapshotsSessionReviewPolicy(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	obs := ports.SCMObservation{
		PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
		Review: ports.SCMReviewObservation{
			Reviews: []ports.SCMReviewSummaryObservation{{ID: "r1", State: string(domain.ReviewChangesRequest), Body: "review body"}},
			Threads: []ports.SCMReviewThreadObservation{{ID: "t1", IsBot: true, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "human", IsBot: false, Body: "inline comment"}}}},
		},
	}
	for _, autoInject := range []bool{false, true} {
		t.Run(fmt.Sprintf("auto_inject_%t", autoInject), func(t *testing.T) {
			_, _, reviews, _, comments := claimRowsFromSCM("mer-1", obs, ports.ReviewWriteReplace, now, domain.SessionRecord{AutoInjectReview: autoInject})
			if len(reviews) != 1 || reviews[0].AutoInjectReview != autoInject {
				t.Fatalf("reviews = %+v, want policy %t", reviews, autoInject)
			}
			if len(comments) != 1 || comments[0].AutoInjectReview != autoInject {
				t.Fatalf("comments = %+v, want policy %t", comments, autoInject)
			}
			if comments[0].IsBot {
				t.Fatalf("human comment in bot-started thread was persisted as bot-authored: %+v", comments[0])
			}
		})
	}
}

func TestClaimPRRejectsScratchProject(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["scratch-1"] = domain.SessionRecord{
		ID:        "scratch-1",
		ProjectID: "scratch",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/scratch-1"},
	}
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: &fakePRClaimer{},
		SCM: fakeSCM{
			obs: ports.SCMObservation{
				Fetched:  true,
				Provider: "github",
				Host:     "github.com",
				Repo:     "acme/repo",
				PR:       ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
			},
		},
	})

	for _, ref := range []string{"https://github.com/acme/repo/pull/7", "7"} {
		t.Run(ref, func(t *testing.T) {
			_, err := svc.ClaimPR(context.Background(), "scratch-1", ref, ClaimPROptions{})
			if !errors.Is(err, ErrSessionNotClaimable) {
				t.Fatalf("ClaimPR scratch err = %v, want ErrSessionNotClaimable", err)
			}
		})
	}
}

func TestClaimPRMapsObserverAndStoreErrors(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}

	cases := []struct {
		name string
		svc  *Service
		want error
	}{
		{"missing scm", NewWithDeps(Deps{Store: st}), ErrSCMUnavailable},
		{"not found error", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{fetchErr: ports.ErrSCMNotFound}}), ErrPRNotFound},
		{"not found placeholder", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{}}), ErrPRNotFound},
		{"closed", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Closed: true}}}}), ErrPRNotOpen},
		{"merged", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Merged: true}}}}), ErrPRNotOpen},
		{"draft merged", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Draft: true, Merged: true}}}}), ErrPRNotOpen},
		{"draft closed", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Draft: true, Closed: true}}}}), ErrPRNotOpen},
		{"active owner", NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{err: ports.PRClaimedByActiveSessionError{Owner: "mer-2"}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}}), ports.ErrPRClaimedByActiveSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: false})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}

	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPassing, UpdatedAt: now}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{PreviousOwner: "mer-2"}}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}})
	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TakenOverFrom) != 1 || res.TakenOverFrom[0] != "mer-2" || len(res.PRs) != 1 || res.PRs[0].URL == "" {
		t.Fatalf("claim result = %+v", res)
	}
}

// TestClaimPRAllowsDraftPR pins the fix for #4171: a draft PR is open work and
// must be claimable, with the draft fact carried onto the persisted PR row and
// the returned read model rather than being rejected as PR_NOT_OPEN.
func TestClaimPRAllowsDraftPR(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	// Stands in for the row the claimer would have written.
	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, Draft: true, CI: domain.CIPending, UpdatedAt: now}

	claimer := &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}}
	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: claimer,
		SCM: fakeSCM{obs: ports.SCMObservation{
			Fetched:  true,
			Provider: "github",
			Host:     "github.com",
			Repo:     "acme/repo",
			PR:       ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Draft: true},
		}},
	})

	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
	if err != nil {
		t.Fatalf("claim draft PR: %v", err)
	}
	if !claimer.called {
		t.Fatal("ClaimPR was not called for a draft PR")
	}
	if !claimer.gotPR.Draft || claimer.gotPR.Merged || claimer.gotPR.Closed {
		t.Fatalf("persisted PR = %+v, want draft preserved and not merged/closed", claimer.gotPR)
	}
	if len(res.PRs) != 1 || res.PRs[0].URL != "https://github.com/acme/repo/pull/7" || !res.PRs[0].Draft {
		t.Fatalf("claim result = %+v, want the draft PR", res.PRs)
	}
}

// A session that already produced artifacts must flip to pr OutputType as
// soon as a PR is claimed, not on the next artifact-output poll tick — the
// same immediacy claiming a PR always had before OutputType was persisted.
func TestClaimPRReconcilesOutputTypeImmediately(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker,
		Metadata:   domain.SessionMetadata{WorkspacePath: "/ws"},
		OutputType: domain.SessionOutputArtifact,
	}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPending}

	reconciler := &fakeOutputTypeReconciler{}
	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}},
		SCM: fakeSCM{obs: ports.SCMObservation{
			Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
			PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
		}},
		OutputTypeReconciler: reconciler,
	})

	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatalf("claim PR: %v", err)
	}
	if len(reconciler.reconciled) != 1 || reconciler.reconciled[0] != "mer-1" {
		t.Fatalf("reconciled = %v, want [mer-1]", reconciler.reconciled)
	}
}

// A reconcile failure must not fail an otherwise-successful claim: the
// artifact-output poller still corrects OutputType on its next tick.
func TestClaimPRSucceedsWhenOutputTypeReconcileFails(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPending}

	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}},
		SCM: fakeSCM{obs: ports.SCMObservation{
			Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
			PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
		}},
		OutputTypeReconciler: &fakeOutputTypeReconciler{err: errors.New("boom")},
	})

	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{}); err != nil {
		t.Fatalf("claim PR should succeed despite reconcile failure: %v", err)
	}
}

func TestClaimPRGitLabMR(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["gl-1"] = domain.SessionRecord{ID: "gl-1", ProjectID: "gl", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["gl"] = domain.ProjectRecord{ID: "gl", RepoOriginURL: "https://gitlab.com/castai/ctxd"}
	st.pr["gl-1"] = domain.PRFacts{URL: "https://gitlab.com/castai/ctxd/-/merge_requests/9", Number: 9, CI: domain.CIPassing, UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)}

	obs := ports.SCMObservation{
		Fetched:  true,
		Provider: "gitlab",
		Host:     "gitlab.com",
		Repo:     "castai/ctxd",
		PR:       ports.SCMPRObservation{URL: "https://gitlab.com/castai/ctxd/-/merge_requests/9", Number: 9},
	}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}}, SCM: fakeSCM{obs: obs}})
	res, err := svc.ClaimPR(context.Background(), "gl-1", "https://gitlab.com/castai/ctxd/-/merge_requests/9", ClaimPROptions{AllowTakeover: true})
	if err != nil {
		t.Fatalf("claim gitlab MR: %v", err)
	}
	if len(res.PRs) != 1 || res.PRs[0].URL != "https://gitlab.com/castai/ctxd/-/merge_requests/9" {
		t.Fatalf("claim result = %+v", res)
	}
}

func TestClaimPRReviewFetchFailureProceedsWithPreserve(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPassing, UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)}
	obs := ports.SCMObservation{
		Fetched:  true,
		Provider: "github",
		Host:     "github.com",
		Repo:     "acme/repo",
		PR:       ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
	}
	claimer := &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}}
	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: claimer,
		SCM: fakeSCM{
			obs:       obs,
			reviewErr: errors.New("review API down"),
		},
	})
	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
	if err != nil {
		t.Fatalf("claim should succeed when FetchReviewThreads fails: %v", err)
	}
	if !claimer.called {
		t.Fatalf("ClaimPR was not called")
	}
	if claimer.gotMode != ports.ReviewWritePreserve {
		t.Fatalf("review mode = %v, want ReviewWritePreserve", claimer.gotMode)
	}
	if len(claimer.gotThreads) != 0 {
		t.Fatalf("no threads should be persisted on fetch failure, got %#v", claimer.gotThreads)
	}
	if len(res.PRs) != 1 || res.PRs[0].URL != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("claim result = %+v", res)
	}
}

func TestClaimPRReviewFetchNotFoundErrors(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	obs := ports.SCMObservation{
		Fetched:  true,
		Provider: "github",
		Host:     "github.com",
		Repo:     "acme/repo",
		PR:       ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
	}
	claimer := &fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{}}}
	svc := NewWithDeps(Deps{
		Store:     st,
		PRClaimer: claimer,
		SCM: fakeSCM{
			obs:       obs,
			reviewErr: ports.ErrSCMNotFound,
		},
	})
	_, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{})
	if !errors.Is(err, ErrPRNotFound) {
		t.Fatalf("err = %v, want ErrPRNotFound", err)
	}
	if claimer.called {
		t.Fatalf("ClaimPR should not be called when PR was deleted")
	}
}

func TestNormalizePRRefGitLab(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		ref        string
		repoOrigin string
		wantURL    string
		wantNum    int
		wantErr    bool
	}{
		{
			name:       "gitlab MR URL",
			ref:        "https://gitlab.com/castai/ctxd/-/merge_requests/9",
			repoOrigin: "https://gitlab.com/castai/ctxd",
			wantURL:    "https://gitlab.com/castai/ctxd/-/merge_requests/9",
			wantNum:    9,
		},
		{
			name:       "gitlab MR URL with nested groups",
			ref:        "https://gitlab.com/group/subgroup/repo/-/merge_requests/42",
			repoOrigin: "https://gitlab.com/group/subgroup/repo",
			wantURL:    "https://gitlab.com/group/subgroup/repo/-/merge_requests/42",
			wantNum:    42,
		},
		{
			name:       "numeric ref with gitlab repo",
			ref:        "9",
			repoOrigin: "https://gitlab.com/castai/ctxd",
			wantURL:    "https://gitlab.com/castai/ctxd/-/merge_requests/9",
			wantNum:    9,
		},
		{
			name:       "github PR URL unchanged",
			ref:        "https://github.com/owner/repo/pull/42",
			repoOrigin: "https://github.com/owner/repo",
			wantURL:    "https://github.com/owner/repo/pull/42",
			wantNum:    42,
		},
		{
			name:       "numeric ref with github repo unchanged",
			ref:        "7",
			repoOrigin: "https://github.com/acme/repo",
			wantURL:    "https://github.com/acme/repo/pull/7",
			wantNum:    7,
		},
		{
			name:       "invalid URL",
			ref:        "https://example.com/foo",
			repoOrigin: "",
			wantErr:    true,
		},
		{
			name:       "empty ref",
			ref:        "",
			repoOrigin: "",
			wantErr:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, n, err := normalizePRRef(tc.ref, tc.repoOrigin)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got url=%s n=%d", url, n)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tc.wantURL || n != tc.wantNum {
				t.Fatalf("got url=%s n=%d, want url=%s n=%d", url, n, tc.wantURL, tc.wantNum)
			}
		})
	}
}

func TestRequireSameRepoGitLab(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		prURL      string
		repoOrigin string
		wantErr    error
	}{
		{"matching gitlab", "https://gitlab.com/castai/ctxd/-/merge_requests/9", "https://gitlab.com/castai/ctxd", nil},
		{"matching github", "https://github.com/owner/repo/pull/42", "https://github.com/owner/repo", nil},
		{"renamed repo, old PR url", "https://github.com/justvibedev/tallpine-dev/pull/7", "https://github.com/justvibedev/tallpine-dev.git", nil},
		{"renamed repo, new PR url", "https://github.com/justvibedev/tallpine-dev/pull/7", "https://github.com/justvibedev/tallpine-dev.git", nil},
		{"renamed repo, other owner", "https://github.com/other/aox/pull/7", "https://github.com/justvibedev/tallpine-dev.git", ErrProjectMismatch},
		{"empty origin rejects", "https://gitlab.com/castai/ctxd/-/merge_requests/9", "", ErrProjectMismatch},
		{"mismatch", "https://gitlab.com/castai/ctxd/-/merge_requests/9", "https://github.com/other/repo", ErrProjectMismatch},
		{"gitlab mismatch repo", "https://gitlab.com/castai/ctxd/-/merge_requests/9", "https://gitlab.com/other/repo", ErrProjectMismatch},
		// Cross-provider mismatch: same owner/repo name on GitHub and GitLab
		// must not validate
		{"cross-provider github origin vs gitlab mr", "https://gitlab.com/owner/repo/-/merge_requests/1", "https://github.com/owner/repo.git", ErrProjectMismatch},
		{"cross-provider gitlab origin vs github pr", "https://github.com/owner/repo/pull/1", "https://gitlab.com/owner/repo.git", ErrProjectMismatch},
		// Cross-host mismatch: gitlab.com origin vs self-managed gitlab MR
		// must not validate even though both are GitLab
		{"cross-host gitlab.com origin vs self-managed mr", "https://gitlab.mycompany.com/owner/repo/-/merge_requests/1", "https://gitlab.com/owner/repo.git", ErrProjectMismatch},
		{"matching self-managed gitlab", "https://gitlab.mycompany.com/eng/team/-/merge_requests/3", "https://gitlab.mycompany.com/eng/team.git", nil},
		// Nested namespaces must match end-to-end
		{"matching nested namespace gitlab", "https://gitlab.com/group/subgroup/repo/-/merge_requests/5", "https://gitlab.com/group/subgroup/repo.git", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := requireSameRepo(tc.prURL, tc.repoOrigin)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestScmRepoForClaimGitLab(t *testing.T) {
	t.Parallel()

	// The SCM target comes from the claimed URL, including its provider.
	repo, err := scmRepoForClaim("https://gitlab.com/castai/ctxd/-/merge_requests/9")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.Provider != "gitlab" || repo.Host != "gitlab.com" || repo.Owner != "castai" || repo.Name != "ctxd" {
		t.Fatalf("repo = %+v", repo)
	}
}

func TestListPRsOrdersActiveBeforeClosedThenUpdatedDesc(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.pr = map[domain.SessionID]domain.PRFacts{}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{URL: "closed-new", SessionID: "mer-1", Number: 1, Closed: true, UpdatedAt: now.Add(2 * time.Hour)},
		{URL: "open-old", SessionID: "mer-1", Number: 2, UpdatedAt: now},
		{URL: "open-new", SessionID: "mer-1", Number: 3, UpdatedAt: now.Add(time.Hour)},
	}}
	got, err := (&Service{store: stList}).ListPRs(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].URL != "open-new" || got[1].URL != "open-old" || got[2].URL != "closed-new" {
		t.Fatalf("order = %+v", got)
	}
}

func TestListPRListingKeepsExternalReportsLinkedAndDedupesTracked(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	st.prs["mer-1"] = []domain.PullRequest{{
		URL: "https://github.com/acme/app/pull/7", SessionID: "mer-1", Number: 7,
		CI: domain.CIUnknown, Review: domain.ReviewNone, Mergeability: domain.MergeUnknown,
		UpdatedAt: time.Now().UTC(),
	}}
	st.reportedPRURLs["mer-1"] = []string{
		"https://www.github.com/ACME/App/pull/007", // tracked through existing SCM facts
		"https://gitlab.com/release/notes/-/merge_requests/9",
		"https://gitlab.com/release/notes/-/merge_requests/9", // report retry
	}
	got, err := (&Service{store: st}).ListPRListing(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tracked) != 1 || len(got.Linked) != 1 || got.Linked[0].URL != "https://gitlab.com/release/notes/-/merge_requests/9" {
		t.Fatalf("listing = %+v", got)
	}
}

func TestListPRSummariesExposesReviewSummariesButKeepsRawLogsAndCommentBodiesPrivate(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/7"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:                      prURL,
		HTMLURL:                  prURL,
		SessionID:                "mer-1",
		Number:                   7,
		CI:                       domain.CIFailing,
		Review:                   domain.ReviewChangesRequest,
		Mergeability:             domain.MergeConflicting,
		Provider:                 "github",
		Repo:                     "acme/repo",
		Title:                    "Fix dashboard",
		Author:                   "ada",
		AuthorAvatarURL:          "https://avatars.githubusercontent.com/u/123?v=4",
		SourceBranch:             "fix/dashboard",
		TargetBranch:             "main",
		HeadSHA:                  "abc123",
		ProviderMergeStateStatus: "dirty",
		UpdatedAt:                now,
		ObservedAt:               now.Add(-time.Minute),
		CIObservedAt:             now.Add(-time.Minute),
		ReviewObservedAt:         now.Add(-time.Minute),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "unit", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://github.com/acme/repo/actions/runs/1", LogTail: "panic: secret"},
		{Name: "lint", Status: domain.PRCheckPassed, Conclusion: "success", URL: "https://github.com/acme/repo/actions/runs/2"},
	}
	stList.reviews[prURL] = []domain.PullRequestReview{
		{ID: "review-1", Author: "reviewer-a", State: domain.ReviewChangesRequest, URL: "https://github.com/acme/repo/pull/7#pullrequestreview-1", Body: "summary: please fix the failing unit test", SubmittedAt: now.Add(-30 * time.Second), AutoInjectReview: false},
	}
	stList.comments[prURL] = []domain.PullRequestComment{
		{Author: "reviewer-a", ReviewID: "4876751117", File: "main.go", Line: 12, Body: "raw body must stay private", URL: "https://github.com/acme/repo/pull/7#discussion_r1", AutoInjectReview: false},
		{Author: "ci-bot", File: "main.go", Line: 13, Body: "bot body", URL: "https://github.com/acme/repo/pull/7#discussion_r2", IsBot: true},
		{Author: "reviewer-a", File: "main.go", Line: 14, Body: "resolved body", URL: "https://github.com/acme/repo/pull/7#discussion_r4", Resolved: true},
		{Author: "reviewer-a", File: "test.go", Line: 22, Body: "another raw body", URL: "https://github.com/acme/repo/pull/7#discussion_r3", AutoInjectReview: true},
	}
	stList.threads[prURL] = []domain.PullRequestReviewThread{
		{ThreadID: "thread-1"},
		{ThreadID: "thread-2"},
		{ThreadID: "thread-3", Resolved: true},
		{ThreadID: "thread-4", IsBot: true},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("summaries = %+v", got)
	}
	pr := got[0]
	if pr.Title != "Fix dashboard" || pr.State != domain.PRStateOpen || pr.Provider != "github" || pr.Repo != "acme/repo" || pr.HeadSHA != "abc123" {
		t.Fatalf("metadata = %+v", pr)
	}
	if pr.Author != "ada" || pr.AuthorAvatarURL != "https://avatars.githubusercontent.com/u/123?v=4" {
		t.Fatalf("author metadata = %+v", pr)
	}
	if len(pr.CI.FailingChecks) != 1 || pr.CI.FailingChecks[0].Name != "unit" || pr.CI.FailingChecks[0].URL == "" {
		t.Fatalf("failing checks = %+v", pr.CI.FailingChecks)
	}
	if pr.Review.Decision != domain.ReviewChangesRequest || !pr.Review.HasUnresolvedHumanComments || pr.Review.UnresolvedThreadCount == nil || *pr.Review.UnresolvedThreadCount != 2 || len(pr.Review.UnresolvedBy) != 1 {
		t.Fatalf("review = %+v", pr.Review)
	}
	if reviewer := pr.Review.UnresolvedBy[0]; reviewer.ReviewerID != "reviewer-a" || reviewer.Count != 2 || len(reviewer.Links) != 2 {
		t.Fatalf("reviewer = %+v", reviewer)
	} else if reviewer.ReviewURL != "https://github.com/acme/repo/pull/7#pullrequestreview-1" {
		t.Fatalf("review url = %q", reviewer.ReviewURL)
	} else if reviewer.Links[0].AutoInjectReview || !reviewer.Links[1].AutoInjectReview {
		t.Fatalf("comment injection decisions = %+v, want false then true", reviewer.Links)
	} else if reviewer.Links[0].ReviewID != "4876751117" || reviewer.Links[1].ReviewID != "" {
		t.Fatalf("comment review ids = %+v, want first linked and second legacy", reviewer.Links)
	} else if reviewer.Links[0].Body != "raw body must stay private" || reviewer.Links[1].Body != "another raw body" {
		t.Fatalf("comment bodies = %+v", reviewer.Links)
	}
	if len(pr.Review.ResolvedBy) != 1 || pr.Review.ResolvedBy[0].Count != 1 || pr.Review.ResolvedBy[0].Links[0].Body != "resolved body" {
		t.Fatalf("resolved comments = %+v", pr.Review.ResolvedBy)
	}
	if pr.Mergeability.State != domain.MergeConflicting || len(pr.Mergeability.ConflictFiles) != 0 || !containsString(pr.Mergeability.Reasons, "conflicts") {
		t.Fatalf("mergeability = %+v", pr.Mergeability)
	}
	if len(pr.Review.Reviews) != 1 {
		t.Fatalf("review summaries = %+v", pr.Review.Reviews)
	}
	if entry := pr.Review.Reviews[0]; entry.Reviewer != "reviewer-a" || entry.Verdict != domain.ReviewChangesRequest ||
		entry.Body != "summary: please fix the failing unit test" ||
		entry.URL != "https://github.com/acme/repo/pull/7#pullrequestreview-1" || entry.AutoInjectReview {
		t.Fatalf("review summary entry = %+v", entry)
	}
	// Human inline review comment bodies are surfaced for display, but bot bodies
	// and CI log tails must still stay out of the PR summary.
	blob := fmt.Sprintf("%+v", got)
	for _, text := range []string{"raw body must stay private", "another raw body"} {
		if !strings.Contains(blob, text) {
			t.Fatalf("summary omitted review comment body %q", text)
		}
	}
	for _, secret := range []string{"bot body", "panic: secret"} {
		if strings.Contains(blob, secret) {
			t.Fatalf("summary leaked private text %q", secret)
		}
	}
}

func TestListPRSummariesThreadCountKnownOnlyForCompleteObservations(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	unresolved := []domain.PullRequestReviewThread{{ThreadID: "thread-1"}, {ThreadID: "thread-2"}}

	cases := []struct {
		name string
		pr   domain.PullRequest
		want *int // nil means the count must stay unknown
	}{
		{
			name: "complete observation publishes the exact count",
			pr:   domain.PullRequest{URL: "https://github.com/acme/repo/pull/1", SessionID: "mer-1", Number: 1, ReviewObservedAt: now},
			want: intPtrForTest(2),
		},
		{
			name: "partial observation omits the count",
			pr:   domain.PullRequest{URL: "https://github.com/acme/repo/pull/2", SessionID: "mer-1", Number: 2, ReviewObservedAt: now, ReviewPartial: true},
			want: nil,
		},
		{
			name: "never-observed reviews omit the count",
			pr:   domain.PullRequest{URL: "https://github.com/acme/repo/pull/3", SessionID: "mer-1", Number: 3},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
			stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{tc.pr}}
			stList.threads[tc.pr.URL] = unresolved

			got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("summaries = %+v", got)
			}
			count := got[0].Review.UnresolvedThreadCount
			if tc.want == nil {
				if count != nil {
					t.Fatalf("unresolvedThreadCount = %d, want unknown (nil)", *count)
				}
				return
			}
			if count == nil || *count != *tc.want {
				t.Fatalf("unresolvedThreadCount = %v, want %d", count, *tc.want)
			}
		})
	}
}

func TestListPRSummariesThreadCountSerializesObservedZero(t *testing.T) {
	t.Parallel()

	// An observed zero must survive the DTO round trip as an explicit 0 rather
	// than being dropped by omitempty.
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	prURL := "https://github.com/acme/repo/pull/4"
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{URL: prURL, SessionID: "mer-1", Number: 4, ReviewObservedAt: now}}}
	stList.threads[prURL] = []domain.PullRequestReviewThread{{ThreadID: "thread-1", Resolved: true}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if count := got[0].Review.UnresolvedThreadCount; count == nil || *count != 0 {
		t.Fatalf("unresolvedThreadCount = %v, want explicit 0", count)
	}
}

func TestListPRSummariesDedupesAliasReviewThreads(t *testing.T) {
	t.Parallel()

	// The same provider thread persisted under two repository-URL aliases of
	// one canonical PR must count once, with the fresher alias record winning
	// a disagreement.
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	oldURL := "https://github.com/acme/repo/pull/9"
	newURL := "https://github.com/acme-corp/repo/pull/9"
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	pr := func(url string) domain.PullRequest {
		return domain.PullRequest{
			URL: url, SessionID: "mer-1", Number: 9, Provider: "github",
			SourceBranch: "fix/alias", HeadSHA: "abc123",
			ReviewObservedAt: now, UpdatedAt: now,
		}
	}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{pr(oldURL), pr(newURL)}}
	// Same unresolved thread under both aliases: one thread, not two.
	stList.threads[oldURL] = []domain.PullRequestReviewThread{{ThreadID: "thread-1", UpdatedAt: now.Add(-time.Hour)}}
	stList.threads[newURL] = []domain.PullRequestReviewThread{{ThreadID: "thread-1", UpdatedAt: now}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("aliases did not group: %+v", got)
	}
	if count := got[0].Review.UnresolvedThreadCount; count == nil || *count != 1 {
		t.Fatalf("unresolvedThreadCount = %v, want 1", count)
	}

	// Conflicting aliases: the old alias still has the thread unresolved, the
	// newer alias records it resolved. The fresher record wins, so the exact
	// count is 0.
	stList.threads[oldURL] = []domain.PullRequestReviewThread{{ThreadID: "thread-1", UpdatedAt: now.Add(-time.Hour)}}
	stList.threads[newURL] = []domain.PullRequestReviewThread{{ThreadID: "thread-1", Resolved: true, UpdatedAt: now}}

	got, err = (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if count := got[0].Review.UnresolvedThreadCount; count == nil || *count != 0 {
		t.Fatalf("unresolvedThreadCount = %v, want 0", count)
	}
}

func TestSummarizeReviewSurfacesSubmittedReviewSummaries(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Second)
	reviews := []domain.PullRequestReview{
		// alice's approved review supersedes her earlier changes_requested one.
		{ID: "a-old", Author: "alice", State: domain.ReviewChangesRequest, Body: "old note", URL: "url-a-old", SubmittedAt: now.Add(-time.Hour)},
		{ID: "a-new", Author: "alice", State: domain.ReviewApproved, Body: "looks good now", URL: "url-a-new", SubmittedAt: now},
		{ID: "b", Author: "bob", State: domain.ReviewChangesRequest, Body: "please fix", URL: "url-b", SubmittedAt: now},
		{ID: "c", Author: "charlie", State: domain.ReviewNone, Body: "non-blocking suggestion", URL: "url-c", SubmittedAt: now},
	}

	got := summarizeReview(domain.PullRequest{URL: "u", Review: domain.ReviewChangesRequest}, nil, reviews, nil, false)

	byReviewer := map[string]PRReviewEntry{}
	for _, entry := range got.Reviews {
		byReviewer[entry.Reviewer] = entry
	}
	if len(got.Reviews) != 3 {
		t.Fatalf("review summaries = %+v, want alice + bob + charlie", got.Reviews)
	}
	if a := byReviewer["alice"]; a.Verdict != domain.ReviewApproved || a.Body != "looks good now" || a.URL != "url-a-new" {
		t.Fatalf("alice entry = %+v, want latest approved with its body", a)
	}
	if b := byReviewer["bob"]; b.Verdict != domain.ReviewChangesRequest || b.Body != "please fix" {
		t.Fatalf("bob entry = %+v, want changes_requested with its body", b)
	}
	if c := byReviewer["charlie"]; c.Verdict != domain.ReviewNone || c.Body != "non-blocking suggestion" || c.URL != "url-c" {
		t.Fatalf("charlie entry = %+v, want commented review with its body", c)
	}
}

func TestListPRSummariesExposesPRLifecycleTimes(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	createdAt := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	readyAt := time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)
	mergedAt := time.Date(2026, 6, 4, 11, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{URL: "draft", SessionID: "mer-1", Number: 1, Draft: true, CreatedAtProvider: createdAt, UpdatedAt: observedAt},
		{URL: "ready", SessionID: "mer-1", Number: 2, StateChangedAt: readyAt, CreatedAtProvider: createdAt, UpdatedAt: observedAt},
		{URL: "merged", SessionID: "mer-1", Number: 3, Merged: true, CreatedAtProvider: createdAt, MergedAtProvider: mergedAt, UpdatedAt: observedAt},
	}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]PRSummary{}
	for _, pr := range got {
		byURL[pr.URL] = pr
	}
	if !byURL["draft"].StateChangedAt.Equal(createdAt) {
		t.Fatalf("draft stateChangedAt = %s, want created time %s", byURL["draft"].StateChangedAt, createdAt)
	}
	if !byURL["ready"].StateChangedAt.Equal(readyAt) {
		t.Fatalf("ready stateChangedAt = %s, want ready/open transition %s", byURL["ready"].StateChangedAt, readyAt)
	}
	if !byURL["merged"].StateChangedAt.Equal(mergedAt) {
		t.Fatalf("merged stateChangedAt = %s, want merged time %s", byURL["merged"].StateChangedAt, mergedAt)
	}
	for _, url := range []string{"draft", "ready", "merged"} {
		if !byURL[url].CreatedAt.Equal(createdAt) {
			t.Fatalf("%s createdAt = %s, want provider creation time %s", url, byURL[url].CreatedAt, createdAt)
		}
	}
}

func TestListPRSummariesSuppressesFailingChecksUnlessCIFailing(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/8"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:       prURL,
		SessionID: "mer-1",
		Number:    8,
		CI:        domain.CIPassing,
		HeadSHA:   "new-sha",
		UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "copy-check", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://github.com/acme/repo/actions/runs/1"},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if got[0].CI.State != domain.CIPassing || len(got[0].CI.FailingChecks) != 0 {
		t.Fatalf("ci summary = %+v", got[0].CI)
	}
}

func TestListPRSummariesExposesPerPRAutoInjectCIPolicy(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{URL: "enabled-failing", SessionID: "mer-1", CI: domain.CIFailing, AutoInjectCI: true},
		{URL: "disabled-failing", SessionID: "mer-1", CI: domain.CIFailing, AutoInjectCI: false},
		{URL: "enabled-passing", SessionID: "mer-1", CI: domain.CIPassing, AutoInjectCI: true},
		{URL: "enabled-merged", SessionID: "mer-1", CI: domain.CIPassing, Merged: true, AutoInjectCI: true},
	}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]PRSummary{}
	for _, pr := range got {
		byURL[pr.URL] = pr
	}
	if !byURL["enabled-failing"].CI.AutoInjectCI || byURL["disabled-failing"].CI.AutoInjectCI {
		t.Fatalf("failing CI policies = enabled:%v disabled:%v", byURL["enabled-failing"].CI.AutoInjectCI, byURL["disabled-failing"].CI.AutoInjectCI)
	}
	if !byURL["enabled-passing"].CI.AutoInjectCI {
		t.Fatal("passing PR lost its enabled CI injection policy")
	}
	if !byURL["enabled-merged"].CI.AutoInjectCI {
		t.Fatal("terminal PR lost its enabled CI injection policy")
	}
}

func TestListPRSummariesFiltersFailedChecksToCurrentHead(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/9"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:       prURL,
		SessionID: "mer-1",
		Number:    9,
		CI:        domain.CIFailing,
		HeadSHA:   "new-sha",
		UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{
		{Name: "old-copy-check", CommitHash: "old-sha", Status: domain.PRCheckFailed, Conclusion: "failure"},
		{Name: "current-lint", CommitHash: "new-sha", Status: domain.PRCheckFailed, Conclusion: "failure"},
	}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	checks := got[0].CI.FailingChecks
	if len(checks) != 1 || checks[0].Name != "current-lint" {
		t.Fatalf("failing checks = %+v", checks)
	}
}

func TestListPRSummariesSuppressesActiveDetailsForClosedOrMergedPRs(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	prURL := "https://github.com/acme/repo/pull/10"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{
		URL:                      prURL,
		SessionID:                "mer-1",
		Number:                   10,
		Merged:                   true,
		CI:                       domain.CIFailing,
		Review:                   domain.ReviewChangesRequest,
		Mergeability:             domain.MergeConflicting,
		ProviderMergeStateStatus: "dirty",
		UpdatedAt:                time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC),
	}}}
	stList.checks[prURL] = []domain.PullRequestCheck{{Name: "unit", Status: domain.PRCheckFailed}}
	stList.comments[prURL] = []domain.PullRequestComment{{Author: "reviewer-a", File: "main.go", Line: 12, URL: "https://github.com/acme/repo/pull/10#discussion_r1"}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	pr := got[0]
	if pr.State != domain.PRStateMerged {
		t.Fatalf("state = %q", pr.State)
	}
	if len(pr.CI.FailingChecks) != 0 || len(pr.Review.UnresolvedBy) != 0 || len(pr.Mergeability.Reasons) != 0 {
		t.Fatalf("active details should be suppressed for merged PR: ci=%+v review=%+v merge=%+v", pr.CI, pr.Review, pr.Mergeability)
	}
}

func TestListPRSummariesOnlyEmitsMergeReasonsForBlockedStates(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{
			URL:                      "mergeable",
			SessionID:                "mer-1",
			Number:                   11,
			CI:                       domain.CIFailing,
			Review:                   domain.ReviewRequired,
			Mergeability:             domain.MergeMergeable,
			ProviderMergeStateStatus: "behind",
			UpdatedAt:                now,
		},
		{
			URL:                      "blocked",
			SessionID:                "mer-1",
			Number:                   12,
			Review:                   domain.ReviewRequired,
			Mergeability:             domain.MergeBlocked,
			ProviderMergeStateStatus: "behind",
			UpdatedAt:                now.Add(time.Minute),
		},
	}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	byNumber := map[int]PRSummary{}
	for _, pr := range got {
		byNumber[pr.Number] = pr
	}
	if reasons := byNumber[11].Mergeability.Reasons; len(reasons) != 0 {
		t.Fatalf("mergeable reasons = %+v", reasons)
	}
	if reasons := byNumber[12].Mergeability.Reasons; !containsString(reasons, "behind_base") || !containsString(reasons, "review_required") {
		t.Fatalf("blocked reasons = %+v", reasons)
	}
}

func TestListPRSummariesCollapsesTransferredRepoAliases(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	oldURL := "https://github.com/AgentWrapper/tallpine-dev/pull/3193"
	newURL := "https://github.com/justvibedev/tallpine-dev/pull/3193"
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{
			URL:          oldURL,
			SessionID:    "mer-1",
			Number:       3193,
			Provider:     "github",
			Host:         "github.com",
			Repo:         "AgentWrapper/tallpine-dev",
			SourceBranch: "tp/mer-1/fix-sigpipe",
			TargetBranch: "main",
			HeadSHA:      "same-head",
			Title:        "old alias",
			CI:           domain.CIFailing,
			UpdatedAt:    now,
		},
		{
			URL:          newURL,
			HTMLURL:      newURL,
			SessionID:    "mer-1",
			Number:       3193,
			Provider:     "github",
			Host:         "github.com",
			Repo:         "justvibedev/tallpine-dev",
			SourceBranch: "tp/mer-1/fix-sigpipe",
			TargetBranch: "main",
			HeadSHA:      "same-head",
			Title:        "new alias",
			CI:           domain.CIFailing,
			UpdatedAt:    now.Add(time.Minute),
		},
	}}
	stList.checks[oldURL] = []domain.PullRequestCheck{{
		Name: "old-check", CommitHash: "same-head", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://ci.example/old",
	}}
	stList.checks[newURL] = []domain.PullRequestCheck{{
		Name: "new-check", CommitHash: "same-head", Status: domain.PRCheckFailed, Conclusion: "failure", URL: "https://ci.example/new",
	}}

	got, err := (&Service{store: stList}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("summaries = %d, want 1: %+v", len(got), got)
	}
	if got[0].URL != newURL || got[0].Title != "new alias" {
		t.Fatalf("summary primary = %q %q, want new alias", got[0].URL, got[0].Title)
	}
	if names := failingCheckNames(got[0].CI.FailingChecks); !sameStrings(names, []string{"old-check", "new-check"}) {
		t.Fatalf("failing checks = %v, want old and new alias checks", names)
	}
}

func TestDeduplicatePRFactsKeepsSameNumberAcrossDifferentRepos(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	got := deduplicatePRFacts([]domain.PRFacts{
		{
			URL:          "https://github.com/acme/api/pull/7",
			Number:       7,
			SourceBranch: "fix-tests",
			UpdatedAt:    now,
		},
		{
			URL:          "https://github.com/acme/web/pull/7",
			Number:       7,
			SourceBranch: "fix-tests",
			UpdatedAt:    now.Add(time.Minute),
		},
	})
	if len(got) != 2 {
		t.Fatalf("facts = %d, want distinct same-number PRs from different repos: %+v", len(got), got)
	}
}

func TestDeduplicatePRFactsKeepsSameBasenameAcrossDifferentOwners(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	got := deduplicatePRFacts([]domain.PRFacts{
		{
			URL:          "https://github.com/acme/repo/pull/7",
			Number:       7,
			SourceBranch: "fix-tests",
			TargetBranch: "main",
			HeadSHA:      "acme-head",
			UpdatedAt:    now,
		},
		{
			URL:          "https://github.com/other/repo/pull/7",
			Number:       7,
			SourceBranch: "fix-tests",
			TargetBranch: "main",
			HeadSHA:      "other-head",
			UpdatedAt:    now.Add(time.Minute),
		},
	})
	if len(got) != 2 {
		t.Fatalf("facts = %d, want distinct same-basename PRs from different owners: %+v", len(got), got)
	}
}

func TestDeduplicatePRFactsCollapsesTransferredRepoAliasesWithSameHead(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	got := deduplicatePRFacts([]domain.PRFacts{
		{
			URL:                      "https://github.com/AgentWrapper/tallpine-dev/pull/3193",
			Number:                   3193,
			ReviewComments:           true,
			ExternalChangesRequested: true,
			ExternalComments:         true,
			SourceBranch:             "tp/mer-1/fix-sigpipe",
			TargetBranch:             "main",
			HeadSHA:                  "same-head",
			UpdatedAt:                now,
		},
		{
			URL:              "https://github.com/justvibedev/tallpine-dev/pull/3193",
			Number:           3193,
			ExternalApproved: true,
			SourceBranch:     "tp/mer-1/fix-sigpipe",
			TargetBranch:     "main",
			HeadSHA:          "same-head",
			UpdatedAt:        now.Add(time.Minute),
		},
	})
	if len(got) != 1 {
		t.Fatalf("facts = %d, want transferred aliases collapsed: %+v", len(got), got)
	}
	if got[0].URL != "https://github.com/justvibedev/tallpine-dev/pull/3193" ||
		!got[0].ReviewComments ||
		!got[0].ExternalChangesRequested ||
		!got[0].ExternalComments ||
		!got[0].ExternalApproved {
		t.Fatalf("merged facts = %+v, want newest URL and preserved comments", got[0])
	}
}

func TestToSessionWithFactsRemapsTransferredAliasReviewRuns(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:                "mer-1",
		ProjectID:         "mer",
		UpdatedAt:         time.Date(2026, 8, 28, 10, 0, 0, 0, time.UTC),
		AutoReviewEnabled: true,
		AutoInjectReview:  true,
	}
	oldURL := "https://github.com/AgentWrapper/tallpine-dev/pull/3193"
	newURL := "https://github.com/justvibedev/tallpine-dev/pull/3193"
	st.prFacts[rec.ID] = []domain.PRFacts{
		{
			URL:                      oldURL,
			Number:                   3193,
			Review:                   domain.ReviewRequired,
			SourceBranch:             "tp/mer-1/fix-sigpipe",
			TargetBranch:             "main",
			HeadSHA:                  "same-head",
			UpdatedAt:                rec.UpdatedAt,
			ExternalChangesRequested: false,
		},
		{
			URL:          newURL,
			Number:       3193,
			Review:       domain.ReviewRequired,
			SourceBranch: "tp/mer-1/fix-sigpipe",
			TargetBranch: "main",
			HeadSHA:      "same-head",
			UpdatedAt:    rec.UpdatedAt.Add(time.Minute),
		},
	}
	st.reviewRuns[rec.ID] = []domain.CurrentHeadReviewRun{{
		SessionID: rec.ID,
		Harness:   "claude-code",
		PRURL:     oldURL,
		Status:    domain.ReviewRunRunning,
		ID:        "run-old",
		CreatedAt: rec.UpdatedAt,
	}}

	sess, err := (&Service{store: st, clock: func() time.Time { return rec.UpdatedAt.Add(2 * time.Minute) }}).toSessionWithFacts(context.Background(), rec, st.prFacts[rec.ID], st.reviewRuns[rec.ID], false, "", true, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.KanbanColumn != "validating" {
		t.Fatalf("column = %q, want validating", sess.KanbanColumn)
	}
	if sess.DisplayStatus != "Reviewing" {
		t.Fatalf("displayStatus = %q, want Reviewing", sess.DisplayStatus)
	}
	if len(sess.PRs) != 1 || sess.PRs[0].URL != newURL {
		t.Fatalf("deduped PRs = %+v, want newest alias only", sess.PRs)
	}
}

func TestToSessionWithFactsCanonicalAliasRunSupersedesOlderAliasRun(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:                "mer-2",
		ProjectID:         "mer",
		UpdatedAt:         time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC),
		AutoReviewEnabled: true,
		AutoInjectReview:  true,
	}
	oldURL := "https://github.com/AgentWrapper/tallpine-dev/pull/4000"
	newURL := "https://github.com/justvibedev/tallpine-dev/pull/4000"
	st.prFacts[rec.ID] = []domain.PRFacts{
		{
			URL:          oldURL,
			Number:       4000,
			Review:       domain.ReviewRequired,
			SourceBranch: "tp/mer-2/fix-review",
			TargetBranch: "main",
			HeadSHA:      "same-head",
			UpdatedAt:    rec.UpdatedAt,
		},
		{
			URL:          newURL,
			Number:       4000,
			Review:       domain.ReviewRequired,
			SourceBranch: "tp/mer-2/fix-review",
			TargetBranch: "main",
			HeadSHA:      "same-head",
			UpdatedAt:    rec.UpdatedAt.Add(time.Minute),
		},
	}
	st.reviewRuns[rec.ID] = []domain.CurrentHeadReviewRun{
		{
			SessionID: rec.ID, Harness: "claude-code", PRURL: oldURL,
			Status: domain.ReviewRunRunning, ID: "run-old", CreatedAt: rec.UpdatedAt,
		},
		{
			SessionID: rec.ID, Harness: "claude-code", PRURL: newURL,
			Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved,
			ID: "run-new", CreatedAt: rec.UpdatedAt.Add(time.Minute),
		},
	}

	sess, err := (&Service{store: st, clock: func() time.Time { return rec.UpdatedAt.Add(2 * time.Minute) }}).toSessionWithFacts(context.Background(), rec, st.prFacts[rec.ID], st.reviewRuns[rec.ID], false, "", true, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.KanbanColumn != "needs_review" {
		t.Fatalf("column = %q, want needs_review", sess.KanbanColumn)
	}
	if sess.DisplayStatus != "Needs human review" {
		t.Fatalf("displayStatus = %q, want Needs human review", sess.DisplayStatus)
	}
}
