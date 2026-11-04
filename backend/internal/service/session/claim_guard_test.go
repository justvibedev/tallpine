package session

import (
	"context"
	"errors"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type guardedFakePRClaimer struct {
	fakePRClaimer
	owner ports.ClaimOwner
	err   error
}

func (f *guardedFakePRClaimer) ClaimPRGuarded(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, reviews []domain.PullRequestReview, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, mode ports.ReviewWriteMode, allow bool, guard func(ports.ClaimOwner) error) (ports.ClaimOutcome, error) {
	if err := guard(f.owner); err != nil {
		f.err = err
		return ports.ClaimOutcome{}, err
	}
	return f.ClaimPR(ctx, pr, checks, reviews, threads, comments, mode, allow)
}

func newGuardClaimFixture(t *testing.T, claimer ports.PRClaimer) *Service {
	t.Helper()
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: t.TempDir()}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}
	return NewWithDeps(Deps{Store: st, PRClaimer: claimer, SCM: fakeSCM{obs: ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo",
		PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7},
	}}})
}

func TestClaimPRTakeoverGuardRunsInTheClaimer(t *testing.T) {
	t.Parallel()

	claimer := &guardedFakePRClaimer{owner: ports.ClaimOwner{SessionID: "web-1", ProjectID: "web"}}
	svc := newGuardClaimFixture(t, claimer)
	veto := errors.New("out of scope")
	_, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true, TakeoverGuard: func(ports.ClaimOwner) error { return veto }})
	if !errors.Is(err, veto) || claimer.called {
		t.Fatalf("vetoed claim: err=%v called=%v", err, claimer.called)
	}
	if _, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true, TakeoverGuard: func(ports.ClaimOwner) error { return nil }}); err != nil || !claimer.called {
		t.Fatalf("allowed claim: err=%v called=%v", err, claimer.called)
	}
}

// A guard the claimer cannot run atomically must not be skipped.
func TestClaimPRTakeoverGuardFailsClosedWithoutGuardedClaimer(t *testing.T) {
	t.Parallel()

	claimer := &fakePRClaimer{}
	svc := newGuardClaimFixture(t, claimer)
	_, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true, TakeoverGuard: func(ports.ClaimOwner) error { return nil }})
	if err == nil || claimer.called {
		t.Fatalf("unguarded claimer: err=%v called=%v, want a refusal before claiming", err, claimer.called)
	}
}
