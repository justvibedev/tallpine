package pr

import (
	"context"
	"errors"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type fakeProjectConfigs struct {
	noCI bool
	runs []domain.ReviewRun
}

func (f fakeProjectConfigs) ListReviewRunsBySession(context.Context, domain.SessionID) ([]domain.ReviewRun, error) {
	return f.runs, nil
}

// tallpineRun is a Tallpine review pass of the fixture PR.
func tallpineRun(id, sha string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict) domain.ReviewRun {
	return domain.ReviewRun{ID: id, SessionID: "sess-1", Harness: "claude-code", PRURL: "https://github.com/acme/widgets/pull/42", TargetSHA: sha, Status: status, Verdict: verdict, CreatedAt: time.Unix(1, 0).UTC()}
}

const fixtureHead = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func approvedNoCI() fakeProjectConfigs {
	return fakeProjectConfigs{noCI: true, runs: []domain.ReviewRun{tallpineRun("r1", fixtureHead, domain.ReviewRunDelivered, domain.VerdictApproved)}}
}

func (f fakeProjectConfigs) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	return domain.SessionRecord{ID: id, ProjectID: "proj"}, true, nil
}

func (f fakeProjectConfigs) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{ID: id, Config: domain.ProjectConfig{NoCI: f.noCI}}, true, nil
}

// noChecksActionFixture is a PR whose provider reported no checks at all for
// the head (the justvibedev/tallpine-dev case): CI is unknown, NoChecks is set.
func noChecksActionFixture() (domain.PullRequest, *fakeSCMAction) {
	pr, scm := mergeableActionFixture()
	pr.SessionID = "sess-1"
	scm.observation.CI = ports.SCMCIObservation{Summary: string(domain.CIUnknown), HeadSHA: pr.HeadSHA, NoChecks: true}
	scm.review = ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)}
	return pr, scm
}

func mergeWithProjects(t *testing.T, pr domain.PullRequest, scm *fakeSCMAction, policy MergePolicyReader) error {
	t.Helper()
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm, MergePolicy: policy})
	_, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA})
	return err
}

func requireBlocker(t *testing.T, err error, want domain.MergeBlocker) {
	t.Helper()
	if !errors.Is(err, ErrPRPreconditions) {
		t.Fatalf("err = %v, want ErrPRPreconditions", err)
	}
	var pe *PreconditionError
	if !errors.As(err, &pe) || pe.Blocker != want {
		t.Fatalf("err = %v, want blocker %q", err, want)
	}
}

func TestActionServiceMerge_NoCIProjectMergesTallpineApprovedHeadWithNoChecks(t *testing.T) {
	pr, scm := noChecksActionFixture()
	if err := mergeWithProjects(t, pr, scm, approvedNoCI()); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if scm.mergeCalls != 1 {
		t.Fatalf("merge calls = %d, want 1", scm.mergeCalls)
	}
}

func TestActionServiceMerge_NoChecksStillBlocksWithoutOptIn(t *testing.T) {
	for name, projects := range map[string]MergePolicyReader{
		"opted out":       fakeProjectConfigs{noCI: false, runs: approvedNoCI().runs},
		"no project info": nil,
	} {
		t.Run(name, func(t *testing.T) {
			pr, scm := noChecksActionFixture()
			requireBlocker(t, mergeWithProjects(t, pr, scm, projects), domain.MergeBlockerCIUnknown)
			if scm.mergeCalls != 0 {
				t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
			}
		})
	}
}

func TestActionServiceMerge_NoCIProjectStillBlocksReportedChecks(t *testing.T) {
	tests := []struct {
		name string
		ci   ports.SCMCIObservation
		want domain.MergeBlocker
	}{
		{"pending", ports.SCMCIObservation{Summary: string(domain.CIPending), Checks: []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckInProgress)}}}, domain.MergeBlockerCIPending},
		{"failing", ports.SCMCIObservation{Summary: string(domain.CIFailing), Checks: []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckFailed)}}}, domain.MergeBlockerCIFailing},
		{"unknown without proof of no checks", ports.SCMCIObservation{Summary: string(domain.CIUnknown)}, domain.MergeBlockerCIUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, scm := noChecksActionFixture()
			tt.ci.HeadSHA = pr.HeadSHA
			scm.observation.CI = tt.ci
			requireBlocker(t, mergeWithProjects(t, pr, scm, approvedNoCI()), tt.want)
		})
	}
}

// With noCI, Tallpine review approval of the exact current head stands in for CI;
// GitHub's reviewDecision (none here) is not enough.
func TestActionServiceMerge_NoCIProjectRequiresTallpineApprovalOfCurrentHead(t *testing.T) {
	const older = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	tests := []struct {
		name string
		runs []domain.ReviewRun
		want domain.MergeBlocker
	}{
		{"no Tallpine run", nil, domain.MergeBlockerReviewApprovalRequired},
		{"Tallpine review running", []domain.ReviewRun{tallpineRun("r1", fixtureHead, domain.ReviewRunRunning, domain.VerdictNone)}, domain.MergeBlockerTallpineReviewRunning},
		{"Tallpine requested changes", []domain.ReviewRun{tallpineRun("r1", fixtureHead, domain.ReviewRunDelivered, domain.VerdictChangesRequested)}, domain.MergeBlockerTallpineReviewChangesRequested},
		{"Tallpine approved an older head", []domain.ReviewRun{tallpineRun("r1", older, domain.ReviewRunDelivered, domain.VerdictApproved)}, domain.MergeBlockerReviewApprovalRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, scm := noChecksActionFixture()
			scm.review = ports.SCMReviewObservation{Decision: string(domain.ReviewNone)}
			requireBlocker(t, mergeWithProjects(t, pr, scm, fakeProjectConfigs{noCI: true, runs: tt.runs}), tt.want)
			if scm.mergeCalls != 0 {
				t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
			}
		})
	}
}

func TestActionServiceMerge_ReportsWhichPreconditionFailed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*fakeSCMAction)
		want   domain.MergeBlocker
	}{
		{"ci unknown", func(s *fakeSCMAction) { s.observation.CI.Summary = string(domain.CIUnknown) }, domain.MergeBlockerCIUnknown},
		{"ci pending", func(s *fakeSCMAction) { s.observation.CI.Summary = string(domain.CIPending) }, domain.MergeBlockerCIPending},
		{"ci failing", func(s *fakeSCMAction) { s.observation.CI.Summary = string(domain.CIFailing) }, domain.MergeBlockerCIFailing},
		{"ci head mismatch", func(s *fakeSCMAction) { s.observation.CI.HeadSHA = "cccccccccccccccccccccccccccccccccccccccc" }, domain.MergeBlockerCIHeadMismatch},
		{"partial review data", func(s *fakeSCMAction) { s.review.Partial = true }, domain.MergeBlockerReviewDataPartial},
		{"changes requested", func(s *fakeSCMAction) { s.review.Decision = string(domain.ReviewChangesRequest) }, domain.MergeBlockerChangesRequested},
		{"unresolved comments", func(s *fakeSCMAction) {
			s.review.Threads = []ports.SCMReviewThreadObservation{{ID: "t1", Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix"}}}}
		}, domain.MergeBlockerUnresolvedComments},
		{"not mergeable", func(s *fakeSCMAction) { s.observation.Mergeability.State = string(domain.MergeBlocked) }, domain.MergeBlockerNotMergeable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, scm := mergeableActionFixture()
			tt.mutate(scm)
			requireBlocker(t, mergeWithProjects(t, pr, scm, nil), tt.want)
			if scm.mergeCalls != 0 {
				t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
			}
		})
	}
}

// GitHub's aggregate reviewDecision is not tied to a commit: without "dismiss
// stale approvals" a human approval of an older head still reads APPROVED
// after a new push. It must not satisfy the noCI gate on its own.
func TestActionServiceMerge_NoCIProjectIgnoresProviderApprovalWithoutTallpineApproval(t *testing.T) {
	const older = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for name, runs := range map[string][]domain.ReviewRun{
		"no Tallpine run":                 nil,
		"Tallpine approved an older head": {tallpineRun("r1", older, domain.ReviewRunDelivered, domain.VerdictApproved)},
	} {
		t.Run(name, func(t *testing.T) {
			pr, scm := noChecksActionFixture()
			scm.review = ports.SCMReviewObservation{
				Decision: string(domain.ReviewApproved),
				Reviews:  []ports.SCMReviewSummaryObservation{{ID: "gh1", Author: "alice", State: string(domain.ReviewApproved), TargetSHA: older}},
			}
			requireBlocker(t, mergeWithProjects(t, pr, scm, fakeProjectConfigs{noCI: true, runs: runs}), domain.MergeBlockerReviewApprovalRequired)
			if scm.mergeCalls != 0 {
				t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
			}
		})
	}
}

func TestActionServiceMerge_NoCIProviderApprovalDoesNotOverrideTallpineChangesRequest(t *testing.T) {
	pr, scm := noChecksActionFixture()
	runs := []domain.ReviewRun{tallpineRun("r1", fixtureHead, domain.ReviewRunDelivered, domain.VerdictChangesRequested)}
	requireBlocker(t, mergeWithProjects(t, pr, scm, fakeProjectConfigs{noCI: true, runs: runs}), domain.MergeBlockerTallpineReviewChangesRequested)
}
