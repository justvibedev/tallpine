package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	reviewcore "tallpine.eu/tallpine/backend/internal/review"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
)

type fakeClaimer struct {
	err  error
	opts sessionsvc.ClaimPROptions
}

func (f *fakeClaimer) ClaimPR(_ context.Context, _ domain.SessionID, _ string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	f.opts = opts
	return sessionsvc.ClaimPRResult{}, f.err
}

// The review refresher reuses the session claim, which never takes a PR over
// from another active session, and reports claim failures as review errors the
// trigger can explain.
func TestReviewPRRefresherClaimsWithoutTakeoverAndTranslatesErrors(t *testing.T) {
	cases := []struct {
		err  error
		want error
		text string
	}{
		{nil, nil, ""},
		{ports.PRClaimedByActiveSessionError{Owner: "mer-2"}, reviewcore.ErrPROwnedElsewhere, "belongs to active session mer-2"},
		{sessionsvc.ErrPRNotFound, reviewcore.ErrNotFound, "not found on the provider"},
		{sessionsvc.ErrPRNotOpen, reviewcore.ErrInvalid, "is not open"},
		{sessionsvc.ErrPRBranchNotSession, reviewcore.ErrInvalid, "does not belong to this session"},
		{sessionsvc.ErrProjectMismatch, reviewcore.ErrInvalid, "this project's repository"},
		{sessionsvc.ErrSCMUnavailable, reviewcore.ErrInvalid, "could not fetch"},
	}
	for _, tc := range cases {
		claimer := &fakeClaimer{err: tc.err}
		err := reviewPRRefresher{sessions: claimer}.RefreshPR(context.Background(), "mer-1", "https://github.com/o/r/pull/1")
		if !claimer.opts.RequireSessionBranch {
			t.Fatal("refresh must only attach PRs on the session's own branch namespace")
		}
		if claimer.opts.AllowTakeover {
			t.Fatal("refresh must never take a PR over from an active session")
		}
		if tc.want == nil {
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			continue
		}
		if !errors.Is(err, tc.want) || !strings.Contains(err.Error(), tc.text) {
			t.Fatalf("claim err %v -> %v, want %v containing %q", tc.err, err, tc.want, tc.text)
		}
	}
}

type discoverClaimer struct{ urls []string }

func (c *discoverClaimer) ClaimPR(_ context.Context, _ domain.SessionID, ref string, _ sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	c.urls = append(c.urls, ref)
	return sessionsvc.ClaimPRResult{}, nil
}

type discoverStore struct{}

func (discoverStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: "mer-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "tp/mer-1/root"}}, true, nil
}

func (discoverStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{RepoOriginURL: "https://github.com/o/r.git"}, true, nil
}

type discoverSCM struct{ pulls []ports.SCMPRObservation }

func (discoverSCM) ParseRepository(string) (ports.SCMRepo, bool) {
	return ports.SCMRepo{Provider: "github", Owner: "o", Name: "r"}, true
}

func (discoverSCM) AuthenticatedIdentityForProvider(context.Context, string, string) (ports.SCMIdentity, error) {
	return ports.SCMIdentity{Login: "me", Human: true}, nil
}

func (d discoverSCM) ListPRsByRepo(context.Context, ports.SCMRepo, time.Time) ([]ports.SCMPRObservation, error) {
	return d.pulls, nil
}

// B320: a PR opened from a sibling branch (not the root branch) is attached;
// other sessions' and closed PRs are not.
func TestReviewPRRefresherDiscoversSiblingBranchPR(t *testing.T) {
	mk := func(n int, branch string) ports.SCMPRObservation {
		return ports.SCMPRObservation{HTMLURL: fmt.Sprintf("https://github.com/o/r/pull/%d", n), Number: n, SourceBranch: branch, Author: "me", HeadRepo: "o/r", BaseRepo: "o/r"}
	}
	closed := mk(4, "tp/mer-1/old")
	closed.Closed = true
	stranger := mk(5, "tp/mer-1/x")
	stranger.Author = "someone-else"
	fork := mk(6, "tp/mer-1/y")
	fork.HeadRepo = "evil/r"
	claimer := &discoverClaimer{}
	r := reviewPRRefresher{sessions: claimer, store: discoverStore{}, scm: discoverSCM{pulls: []ports.SCMPRObservation{
		mk(1, "tp/mer-1/b308-prepared"), mk(2, "tp/mer-2/root"), mk(3, "feature/x"), closed, stranger, fork,
	}}}
	if err := r.DiscoverPRs(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if len(claimer.urls) != 1 || claimer.urls[0] != "https://github.com/o/r/pull/1" {
		t.Fatalf("claimed %v, want only the sibling-branch PR #1", claimer.urls)
	}
}

// With no SCM provider configured discovery is a no-op, not a nil-pointer panic.
func TestReviewPRRefresherDiscoverWithoutProviderIsNoop(t *testing.T) {
	if err := (reviewPRRefresher{sessions: &discoverClaimer{}}).DiscoverPRs(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
}
