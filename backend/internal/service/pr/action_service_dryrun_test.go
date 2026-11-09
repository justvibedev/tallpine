package pr

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestActionServiceMerge_DryRunChecksPreconditionsWithoutMerging(t *testing.T) {
	pr, scm := mergeableActionFixture()
	svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm})
	result, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, DryRun: true})
	if err != nil || !result.DryRun || scm.mergeCalls != 0 {
		t.Fatalf("result=%#v err=%v calls=%d", result, err, scm.mergeCalls)
	}
	_, err = svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: "0000000000000000000000000000000000000000", DryRun: true})
	if err == nil {
		t.Fatal("dry run must still enforce the head guard")
	}
}

// B597: the dry run must reach the NoCI review gate, so it answers what the real
// merge would, including for a head approved only before a rebase.
func TestActionServiceMerge_DryRunAppliesNoCIGate(t *testing.T) {
	const older = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, tt := range []struct {
		name string
		runs []domain.ReviewRun
		want domain.MergeBlocker
	}{
		{"approved only an older head", []domain.ReviewRun{tallpineRun("r1", older, domain.ReviewRunDelivered, domain.VerdictApproved)}, domain.MergeBlockerReviewApprovalRequired},
		{"approved the current head", []domain.ReviewRun{tallpineRun("r1", fixtureHead, domain.ReviewRunDelivered, domain.VerdictApproved)}, domain.MergeBlockerNone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pr, scm := noChecksActionFixture()
			scm.review = ports.SCMReviewObservation{Decision: string(domain.ReviewNone)}
			svc := NewActionService(ActionDeps{Store: &fakeActionStore{pr: pr, ok: true}, Reader: scm, Merger: scm, MergePolicy: fakeProjectConfigs{noCI: true, runs: tt.runs}})
			_, err := svc.Merge(context.Background(), MergeRequest{PRID: "42", PRURL: pr.URL, ExpectedHeadSHA: pr.HeadSHA, DryRun: true})
			if tt.want == domain.MergeBlockerNone {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				requireBlocker(t, err, tt.want)
			}
			if scm.mergeCalls != 0 {
				t.Fatalf("merge calls = %d, want 0", scm.mergeCalls)
			}
		})
	}
}
