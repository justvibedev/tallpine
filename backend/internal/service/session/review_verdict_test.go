package session

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

const testBranch = "tp/s1"

func branchRun(sha string, verdict domain.ReviewVerdict, age int) domain.ReviewRun {
	return domain.ReviewRun{
		PRURL: domain.LocalReviewTargetPrefix + testBranch, TargetSHA: sha, Verdict: verdict,
		CreatedAt: time.Unix(1000+int64(age), 0),
	}
}

func branchRec(head string) domain.SessionRecord {
	return domain.SessionRecord{
		ID: "s1", Metadata: domain.SessionMetadata{Branch: testBranch},
		BranchState: &domain.SessionBranchState{Commits: 2, Head: head},
	}
}

// The wire verdict follows the land gate's rule: the newest verdict on the
// branch's own review key, valid only at the current tip.
func TestBranchReviewVerdictMatchesLandGate(t *testing.T) {
	t.Parallel()
	approved, changes := domain.VerdictApproved, domain.VerdictChangesRequested
	other := branchRun("h2", approved, 9)
	other.PRURL = domain.LocalReviewTargetPrefix + "tp/other"
	cases := []struct {
		name string
		runs []domain.ReviewRun
		head string
		want domain.ReviewVerdict
	}{
		{"approved at head", []domain.ReviewRun{branchRun("h2", approved, 1)}, "h2", approved},
		{"approval of an older head is stale", []domain.ReviewRun{branchRun("h1", approved, 1)}, "h2", domain.VerdictNone},
		{"newest verdict wins even across reviewers", []domain.ReviewRun{branchRun("h2", changes, 1), branchRun("h2", approved, 2)}, "h2", approved},
		{"a later changes request overrides an approval", []domain.ReviewRun{branchRun("h2", approved, 1), branchRun("h2", changes, 2)}, "h2", changes},
		{"another branch's review is ignored", []domain.ReviewRun{other}, "h2", domain.VerdictNone},
		{"no verdict", nil, "h2", domain.VerdictNone},
		{"no head", []domain.ReviewRun{branchRun("", approved, 1)}, "", domain.VerdictNone},
	}
	for _, tc := range cases {
		if got := branchReviewVerdict(tc.runs, branchRec(tc.head)); got != tc.want {
			t.Errorf("%s: verdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestReviewAndLand(t *testing.T) {
	t.Parallel()
	runs := []domain.ReviewRun{branchRun("h2", domain.VerdictApproved, 1)}

	verdict, landed := reviewAndLand(branchRec("h2"), nil, nil, runs)
	if verdict != domain.VerdictApproved || landed {
		t.Fatalf("approved unlanded branch = (%q, %v)", verdict, landed)
	}
	rec := branchRec("h2")
	rec.BranchState.Landed = true
	if verdict, landed = reviewAndLand(rec, nil, nil, runs); verdict != domain.VerdictNone || !landed {
		t.Fatalf("landed branch = (%q, %v), want no verdict and landed", verdict, landed)
	}
	rec = branchRec("h2")
	rec.IsTerminated = true
	if verdict, _ = reviewAndLand(rec, nil, nil, runs); verdict != domain.VerdictNone {
		t.Fatalf("terminated session carries verdict %q", verdict)
	}

	prRuns := []domain.CurrentHeadReviewRun{{Harness: "claude", Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}}
	pr := domain.SessionRecord{ID: "s1"}
	if verdict, landed = reviewAndLand(pr, []domain.PRFacts{{URL: "u"}}, prRuns, nil); verdict != domain.VerdictApproved || landed {
		t.Fatalf("open approved PR = (%q, %v)", verdict, landed)
	}
	if _, landed = reviewAndLand(pr, []domain.PRFacts{{URL: "u", Merged: true}}, prRuns, nil); !landed {
		t.Fatal("merged PR did not read as landed")
	}
}
