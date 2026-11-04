package session

import (
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestMergeabilityReasonsNamesNoCIReviewGate(t *testing.T) {
	pr := domain.PullRequest{
		CI:                       domain.CIUnknown,
		Mergeability:             domain.MergeBlocked,
		ProviderMergeable:        "MERGEABLE",
		ProviderMergeStateStatus: "CLEAN",
		CIHash:                   "abc" + domain.NoCIAwaitingApprovalHashSuffix,
	}
	got := mergeabilityReasons(pr)
	if !slices.Contains(got, domain.MergeReasonTallpineReviewRequired) || slices.Contains(got, "blocked_by_provider") {
		t.Fatalf("reasons = %v, want ao_review_required instead of blocked_by_provider", got)
	}
}
