package session

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestClaimPRSSHPortPreserved(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "ssh://git@gitlab.example.com:8443/alice/repo.git"}
	scm := &claimTargetSCM{}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: &fakePRClaimer{}})
	_, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{})
	if err != nil {
		t.Fatal(err)
	}
	if scm.refs[0].Repo.Host != "gitlab.example.com:8443" {
		t.Fatalf("claim changed SCM authority to %q; expected explicit origin port 8443", scm.refs[0].Repo.Host)
	}
}
func TestClaimPRHTTPSPortOriginStillClaimable(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://gitlab.example.com:8443/alice/repo.git"}
	svc := NewWithDeps(Deps{Store: st, SCM: &claimTargetSCM{}, PRClaimer: &fakePRClaimer{}})
	_, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{})
	if err != nil {
		t.Fatalf("previously supported HTTPS origin is no longer claimable: %v", err)
	}
}

func TestClaimPRReviewCompletenessFollowsReviewFetch(t *testing.T) {
	t.Parallel()

	newDeps := func(scm fakeSCM, claimer *fakePRClaimer) Deps {
		st := newFakeStore()
		st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
		st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
		return Deps{Store: st, SCM: scm, PRClaimer: claimer}
	}
	obs := ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
		PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
	}

	t.Run("partial fetch claims partial certainty", func(t *testing.T) {
		claimer := &fakePRClaimer{}
		scm := fakeSCM{obs: obs, review: ports.SCMReviewObservation{Partial: true}}
		svc := NewWithDeps(newDeps(scm, claimer))
		if _, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{}); err != nil {
			t.Fatal(err)
		}
		if claimer.gotMode != ports.ReviewWriteMerge {
			t.Fatalf("mode = %v, want merge", claimer.gotMode)
		}
		if !claimer.gotPR.ReviewPartial || claimer.gotPR.ReviewObservedAt.IsZero() {
			t.Fatalf("partial claim must store (observed, partial=true), got (%v, %t)",
				claimer.gotPR.ReviewObservedAt, claimer.gotPR.ReviewPartial)
		}
	})

	t.Run("full fetch claims exact certainty", func(t *testing.T) {
		claimer := &fakePRClaimer{}
		scm := fakeSCM{obs: obs, review: ports.SCMReviewObservation{}}
		svc := NewWithDeps(newDeps(scm, claimer))
		if _, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{}); err != nil {
			t.Fatal(err)
		}
		if claimer.gotMode != ports.ReviewWriteReplace {
			t.Fatalf("mode = %v, want replace", claimer.gotMode)
		}
		if claimer.gotPR.ReviewPartial || claimer.gotPR.ReviewObservedAt.IsZero() {
			t.Fatalf("full claim must store (observed, partial=false), got (%v, %t)",
				claimer.gotPR.ReviewObservedAt, claimer.gotPR.ReviewPartial)
		}
	})

	t.Run("failed review fetch offers no certainty", func(t *testing.T) {
		claimer := &fakePRClaimer{}
		scm := fakeSCM{obs: obs, reviewErr: errors.New("review window unavailable")}
		svc := NewWithDeps(newDeps(scm, claimer))
		if _, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{}); err != nil {
			t.Fatal(err)
		}
		if claimer.gotMode != ports.ReviewWritePreserve {
			t.Fatalf("mode = %v, want preserve", claimer.gotMode)
		}
		if !claimer.gotPR.ReviewObservedAt.IsZero() || claimer.gotPR.ReviewPartial {
			t.Fatalf("claim with failed review fetch must pass the zero pair so the upsert keeps stored certainty, got (%v, %t)",
				claimer.gotPR.ReviewObservedAt, claimer.gotPR.ReviewPartial)
		}
	})
}

func TestClaimPRRequireSessionBranch(t *testing.T) {
	t.Parallel()

	run := func(head string) (*fakePRClaimer, error) {
		st := newFakeStore()
		st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws", Branch: "tp/demo-1/root"}}
		st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
		claimer := &fakePRClaimer{}
		scm := fakeSCM{obs: ports.SCMObservation{
			Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
			PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, SourceBranch: head},
		}}
		svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
		_, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{RequireSessionBranch: true})
		return claimer, err
	}

	claimer, err := run("tp/demo-2/topic")
	if !errors.Is(err, ErrPRBranchNotSession) || claimer.called {
		t.Fatalf("foreign branch: err=%v called=%v, want refusal and no claim write", err, claimer.called)
	}
	claimer, err = run("tp/demo-1/follow-up")
	if err != nil || !claimer.called {
		t.Fatalf("own namespace: err=%v called=%v, want claimed", err, claimer.called)
	}
}

func TestClaimPRRequireSessionBranchSkippedForOwnedPR(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws", Branch: "tp/demo-1/root"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
	st.prs["demo-1"] = []domain.PullRequest{{SessionID: "demo-1", URL: "https://github.com/acme/repo/pull/7", Number: 7}}
	claimer := &fakePRClaimer{}
	scm := fakeSCM{obs: ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
		PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, SourceBranch: "other/branch"},
	}}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
	if _, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{RequireSessionBranch: true}); err != nil || !claimer.called {
		t.Fatalf("already-owned PR refresh: err=%v called=%v, want refreshed", err, claimer.called)
	}
}

// A URL pasted from the browser (files tab, query, fragment, mixed case) must
// claim the same PR as the canonical URL.
func TestClaimPRPastedBrowserURL(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo.git"}
	scm := &claimTargetSCM{}
	svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: &fakePRClaimer{}})
	if _, err := svc.ClaimPR(context.Background(), "demo-1", "https://www.GitHub.com/Acme/Repo/pull/7/files?w=1#diff-abc", ClaimPROptions{}); err != nil {
		t.Fatalf("pasted browser URL rejected: %v", err)
	}
	ref := scm.refs[0]
	if ref.Number != 7 || ref.Repo.Host != "github.com" || ref.Repo.Repo != "acme/repo" {
		t.Fatalf("claimed %+v, want github.com acme/repo #7", ref)
	}
}

// Numeric and pasted-URL claims of the same PR on a mixed-case repo resolve to
// one URL, and a legacy stored row (original case) still counts as owned.
func TestClaimPRMixedCaseRepoIdentity(t *testing.T) {
	t.Parallel()

	const origin = "https://github.com/Acme/Repo.git"
	numeric, _, err := normalizePRRef("7", origin)
	if err != nil {
		t.Fatal(err)
	}
	pasted, _, err := normalizePRRef("https://github.com/Acme/Repo/pull/7/files", origin)
	if err != nil {
		t.Fatal(err)
	}
	if numeric != pasted {
		t.Fatalf("numeric %q != pasted %q", numeric, pasted)
	}
	st := newFakeStore()
	st.prs = map[domain.SessionID][]domain.PullRequest{"demo-1": {{URL: "https://github.com/Acme/Repo/pull/7"}}}
	svc := NewWithDeps(Deps{Store: st})
	if !svc.sessionOwnsPR(context.Background(), "demo-1", numeric) {
		t.Fatal("legacy mixed-case row not recognised as owned")
	}
	got := claimedFirst([]domain.PRFacts{{URL: "https://github.com/acme/other/pull/1"}, {URL: "https://github.com/Acme/Repo/pull/7"}}, numeric)
	if got[0].URL != "https://github.com/Acme/Repo/pull/7" {
		t.Fatalf("claimedFirst did not move the legacy row: %+v", got)
	}
}

func TestClaimPRRefuseOtherSessionBranch(t *testing.T) {
	t.Parallel()

	run := func(head string) (*fakePRClaimer, error) {
		st := newFakeStore()
		st.sessions["demo-1"] = domain.SessionRecord{ID: "demo-1", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws", Branch: "tp/demo-1/root"}}
		st.sessions["demo-2"] = domain.SessionRecord{ID: "demo-2", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws2", Branch: "tp/demo-2/root"}}
		// B324 shape: the other session sits on a custom branch name that does not contain its id.
		st.sessions["demo-3"] = domain.SessionRecord{ID: "demo-3", ProjectID: "demo", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws3", Branch: "tp/fix-goimports-red/root"}}
		st.projects["demo"] = domain.ProjectRecord{ID: "demo", RepoOriginURL: "https://github.com/acme/repo"}
		claimer := &fakePRClaimer{}
		scm := fakeSCM{obs: ports.SCMObservation{
			Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
			PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, SourceBranch: head},
		}}
		svc := NewWithDeps(Deps{Store: st, SCM: scm, PRClaimer: claimer})
		_, err := svc.ClaimPR(context.Background(), "demo-1", "7", ClaimPROptions{RefuseOtherSessionBranch: true})
		return claimer, err
	}

	if claimer, err := run("tp/demo-2/topic"); !errors.Is(err, ErrPRBranchNotSession) || claimer.called {
		t.Fatalf("open PR on another session's branch: err=%v called=%v, want refusal", err, claimer.called)
	}
	if claimer, err := run("tp/fix-goimports-red/root"); !errors.Is(err, ErrPRBranchNotSession) || claimer.called {
		t.Fatalf("PR on another session's custom branch: err=%v called=%v, want refusal", err, claimer.called)
	}
	for _, head := range []string{"tp/demo-1/follow-up", "feature/own-worktree-branch"} {
		if claimer, err := run(head); err != nil || !claimer.called {
			t.Fatalf("%s: err=%v called=%v, want claimed", head, err, claimer.called)
		}
	}
}
