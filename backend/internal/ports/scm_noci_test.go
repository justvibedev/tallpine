package ports

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestApplyNoCIPolicy(t *testing.T) {
	noChecks := SCMObservation{Fetched: true, CI: SCMCIObservation{Summary: string(domain.CIUnknown), NoChecks: true}}
	if got := ApplyNoCIPolicy(noChecks, true, domain.TallpineReviewGateApproved).CI.Summary; got != string(domain.CIPassing) {
		t.Fatalf("opted-in no-checks summary = %q, want passing", got)
	}
	if got := ApplyNoCIPolicy(noChecks, false, domain.TallpineReviewGateApproved).CI.Summary; got != string(domain.CIUnknown) {
		t.Fatalf("opted-out no-checks summary = %q, want unknown", got)
	}
	if got := ApplyNoCIPolicy(noChecks, true, domain.TallpineReviewGateMissing).CI.Summary; got != string(domain.CIUnknown) {
		t.Fatalf("opted-in no-checks summary without Tallpine approval = %q, want unknown", got)
	}
	unfetched := noChecks
	unfetched.Fetched = false
	if got := ApplyNoCIPolicy(unfetched, true, domain.TallpineReviewGateApproved).CI.Summary; got != string(domain.CIUnknown) {
		t.Fatalf("unfetched summary = %q, want unknown", got)
	}
	withChecks := noChecks
	withChecks.CI.Checks = []SCMCheckObservation{{Name: "build"}}
	if got := ApplyNoCIPolicy(withChecks, true, domain.TallpineReviewGateApproved).CI.Summary; got != string(domain.CIUnknown) {
		t.Fatalf("summary with visible checks = %q, want unknown", got)
	}
}

func approved(obs SCMObservation) SCMObservation {
	return ApplyNoCIPolicy(obs, true, domain.TallpineReviewGateApproved)
}

func TestApplyNoCIPolicyHoldsBackMergeabilityUntilApproved(t *testing.T) {
	obs := SCMObservation{
		Fetched:      true,
		CI:           SCMCIObservation{Summary: string(domain.CIUnknown), NoChecks: true},
		Mergeability: SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
	held := ApplyNoCIPolicy(obs, true, domain.TallpineReviewGateMissing)
	if held.Mergeability.State != string(domain.MergeBlocked) || held.Mergeability.Mergeable || !held.CI.NoCIAwaitingApproval {
		t.Fatalf("unapproved = %+v / %+v, want blocked and awaiting approval", held.Mergeability, held.CI)
	}
	if !held.Mergeability.HeldByNoCI() || approved(obs).Mergeability.HeldByNoCI() {
		t.Fatalf("HeldByNoCI: held=%v", held.Mergeability.HeldByNoCI())
	}
	if len(held.Mergeability.Blockers) != 1 || held.Mergeability.Blockers[0] != domain.MergeReasonTallpineReviewRequired {
		t.Fatalf("blockers = %v", held.Mergeability.Blockers)
	}
	approved := ApplyNoCIPolicy(obs, true, domain.TallpineReviewGateApproved)
	if approved.Mergeability.State != string(domain.MergeMergeable) || approved.CI.NoCIAwaitingApproval || !approved.CI.NoCIWaived {
		t.Fatalf("approved = %+v / %+v, want provider mergeability kept", approved.Mergeability, approved.CI)
	}
	optedOut := ApplyNoCIPolicy(obs, false, domain.TallpineReviewGateMissing)
	if optedOut.Mergeability.State != string(domain.MergeMergeable) || optedOut.CI.NoCIAwaitingApproval {
		t.Fatalf("opted out = %+v / %+v, want untouched", optedOut.Mergeability, optedOut.CI)
	}
	conflicting := obs
	conflicting.Mergeability = SCMMergeabilityObservation{State: string(domain.MergeConflicting), Conflict: true}
	if got := ApplyNoCIPolicy(conflicting, true, domain.TallpineReviewGateMissing).Mergeability.State; got != string(domain.MergeConflicting) {
		t.Fatalf("conflicting state = %q, want kept", got)
	}
}
