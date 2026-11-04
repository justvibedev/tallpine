package domain

import (
	"strings"
	"testing"
	"time"
)

func TestMergeReadinessBlockerNamesFirstFailedPrecondition(t *testing.T) {
	ready := MergeReadiness{CI: CIPassing, Review: ReviewApproved, Mergeability: MergeMergeable}
	tests := []struct {
		name   string
		mutate func(*MergeReadiness)
		want   MergeBlocker
	}{
		{"ready", func(*MergeReadiness) {}, MergeBlockerNone},
		{"merged", func(r *MergeReadiness) { r.Merged = true }, MergeBlockerMerged},
		{"closed", func(r *MergeReadiness) { r.Closed = true }, MergeBlockerClosed},
		{"draft", func(r *MergeReadiness) { r.Draft = true }, MergeBlockerDraft},
		{"ci empty", func(r *MergeReadiness) { r.CI = "" }, MergeBlockerCIUnknown},
		{"ci unknown", func(r *MergeReadiness) { r.CI = CIUnknown }, MergeBlockerCIUnknown},
		{"ci pending", func(r *MergeReadiness) { r.CI = CIPending }, MergeBlockerCIPending},
		{"ci failing", func(r *MergeReadiness) { r.CI = CIFailing }, MergeBlockerCIFailing},
		{"changes requested", func(r *MergeReadiness) { r.Review = ReviewChangesRequest }, MergeBlockerChangesRequested},
		{"unresolved comments", func(r *MergeReadiness) { r.UnresolvedComments = true }, MergeBlockerUnresolvedComments},
		{"not mergeable", func(r *MergeReadiness) { r.Mergeability = MergeBlocked }, MergeBlockerNotMergeable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := ready
			tt.mutate(&r)
			if got := r.Blocker(); got != tt.want {
				t.Fatalf("Blocker() = %q, want %q", got, tt.want)
			}
			if r.ReadyToMerge() != (tt.want == MergeBlockerNone) {
				t.Fatalf("ReadyToMerge() = %v disagrees with Blocker() %q", r.ReadyToMerge(), tt.want)
			}
			if tt.want != MergeBlockerNone && tt.want.Describe() == string(tt.want) {
				t.Fatalf("blocker %q has no description", tt.want)
			}
		})
	}
}

func TestCIWithNoCIPolicy(t *testing.T) {
	tests := []struct {
		name           string
		ci             CIState
		noChecks       bool
		projectNoCI    bool
		tallpineReview TallpineReviewGate
		want           CIState
	}{
		{"opted in, no checks, Tallpine approved head", CIUnknown, true, true, TallpineReviewGateApproved, CIPassing},
		{"opted in, empty state, Tallpine approved head", "", true, true, TallpineReviewGateApproved, CIPassing},
		{"opted in, no checks, no Tallpine review", CIUnknown, true, true, TallpineReviewGateMissing, CIUnknown},
		{"opted in, no checks, Tallpine review running", CIUnknown, true, true, TallpineReviewGateRunning, CIUnknown},
		{"opted in, no checks, Tallpine requested changes", CIUnknown, true, true, TallpineReviewGateChangesRequested, CIUnknown},
		{"not opted in keeps unknown", CIUnknown, true, false, TallpineReviewGateApproved, CIUnknown},
		{"opted in but checks not proven absent", CIUnknown, false, true, TallpineReviewGateApproved, CIUnknown},
		{"opted in never hides pending", CIPending, true, true, TallpineReviewGateApproved, CIPending},
		{"opted in never hides failing", CIFailing, true, true, TallpineReviewGateApproved, CIFailing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CIWithNoCIPolicy(tt.ci, tt.noChecks, tt.projectNoCI, tt.tallpineReview); got != tt.want {
				t.Fatalf("CIWithNoCIPolicy = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTallpineReviewGateForHead(t *testing.T) {
	const (
		prURL = "https://github.com/acme/widgets/pull/42"
		head  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		older = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	at := func(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
	run := func(id string, harness ReviewerHarness, sha string, status ReviewRunStatus, verdict ReviewVerdict, sec int64) ReviewRun {
		return ReviewRun{ID: id, Harness: harness, PRURL: prURL, TargetSHA: sha, Status: status, Verdict: verdict, CreatedAt: at(sec)}
	}
	tests := []struct {
		name string
		runs []ReviewRun
		want TallpineReviewGate
	}{
		{"no Tallpine run", nil, TallpineReviewGateMissing},
		{"approved current head", []ReviewRun{run("r1", "claude-code", head, ReviewRunComplete, VerdictApproved, 1)}, TallpineReviewGateApproved},
		{"approval of an older head only", []ReviewRun{run("r1", "claude-code", older, ReviewRunDelivered, VerdictApproved, 1)}, TallpineReviewGateMissing},
		{"running on current head", []ReviewRun{run("r1", "claude-code", head, ReviewRunRunning, VerdictNone, 1)}, TallpineReviewGateRunning},
		{"changes requested on current head", []ReviewRun{run("r1", "claude-code", head, ReviewRunDelivered, VerdictChangesRequested, 1)}, TallpineReviewGateChangesRequested},
		{"rerun approval supersedes earlier changes request", []ReviewRun{
			run("r1", "claude-code", head, ReviewRunDelivered, VerdictChangesRequested, 1),
			run("r2", "claude-code", head, ReviewRunComplete, VerdictApproved, 2),
		}, TallpineReviewGateApproved},
		{"rerun still running after approval", []ReviewRun{
			run("r1", "claude-code", head, ReviewRunComplete, VerdictApproved, 1),
			run("r2", "claude-code", head, ReviewRunRunning, VerdictNone, 2),
		}, TallpineReviewGateRunning},
		{"another reviewer requested changes", []ReviewRun{
			run("r1", "claude-code", head, ReviewRunComplete, VerdictApproved, 1),
			run("r2", "codex", head, ReviewRunDelivered, VerdictChangesRequested, 1),
		}, TallpineReviewGateChangesRequested},
		{"failed pass carries no verdict", []ReviewRun{run("r1", "claude-code", head, ReviewRunFailed, VerdictNone, 1)}, TallpineReviewGateMissing},
		{"other PR's approval ignored", []ReviewRun{{ID: "r1", Harness: "claude-code", PRURL: "https://github.com/acme/widgets/pull/7", TargetSHA: head, Status: ReviewRunComplete, Verdict: VerdictApproved}}, TallpineReviewGateMissing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TallpineReviewGateForHead(tt.runs, prURL, head); got != tt.want {
				t.Fatalf("TallpineReviewGateForHead = %q, want %q", got, tt.want)
			}
		})
	}
	if got := TallpineReviewGateForHead([]ReviewRun{run("r1", "claude-code", head, ReviewRunComplete, VerdictApproved, 1)}, prURL, ""); got != TallpineReviewGateMissing {
		t.Fatalf("unknown head gate = %q, want missing", got)
	}
}

func TestMissingTallpineApprovalBlocker(t *testing.T) {
	if TallpineReviewGateMissing.Blocker() != MergeBlockerReviewApprovalRequired || MergeBlockerReviewApprovalRequired != "review_approval_required" {
		t.Fatalf("missing approval blocker = %q", TallpineReviewGateMissing.Blocker())
	}
	if TallpineReviewGateApproved.Blocker() != MergeBlockerNone {
		t.Fatalf("approved gate blocker = %q, want none", TallpineReviewGateApproved.Blocker())
	}
}

func TestNoCIReviewBlockersUseProductName(t *testing.T) {
	for _, b := range []MergeBlocker{MergeBlockerReviewApprovalRequired, MergeBlockerTallpineReviewRunning, MergeBlockerTallpineReviewChangesRequested} {
		if d := b.Describe(); !strings.Contains(d, "Tallpine review") || strings.Contains(d, "AO ") {
			t.Fatalf("%s: %q should name the product", b, d)
		}
	}
}
