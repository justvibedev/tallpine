package review

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCarryApprovalAcrossIdenticalPatch(t *testing.T) {
	const url = "https://github.com/o/r/pull/1"
	for _, tc := range []struct {
		name      string
		enabled   bool
		prior     domain.ReviewRun
		patchIDs  map[string]string
		wantCarry bool
		extra     []domain.ReviewRun
	}{
		{"identical patch carries the approval", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, map[string]string{"sha1": "p", "old": "p"}, true, nil},
		{"approval by another reviewer carries to a risk-picked one (B546)", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved, Harness: domain.ReviewerCodex}, map[string]string{"sha1": "p", "old": "p"}, true, nil},
		{"changed patch is reviewed again", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, map[string]string{"sha1": "p2", "old": "p"}, false, nil},
		{"option off is reviewed again", false, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, map[string]string{"sha1": "p", "old": "p"}, false, nil},
		{"later changes requested blocks an older approval", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, map[string]string{"sha1": "p", "old": "p"}, false, []domain.ReviewRun{
			{ID: "r-mid", ReviewID: "rv1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, PRURL: url, TargetSHA: "mid", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: time.Now().Add(-30 * time.Minute)},
		}},
		{"another reviewer's changes requested on the prior head blocks the carry", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictApproved}, map[string]string{"sha1": "p", "old": "p"}, false, []domain.ReviewRun{
			{ID: "r-codex", ReviewID: "rv2", SessionID: "mer-1", Harness: domain.ReviewerCodex, PRURL: url, TargetSHA: "old", Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested, CreatedAt: time.Now().Add(-2 * time.Hour)},
		}},
		{"changes requested is never carried", true, domain.ReviewRun{Status: domain.ReviewRunComplete, Verdict: domain.VerdictChangesRequested}, map[string]string{"sha1": "p", "old": "p"}, false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior := tc.prior
			prior.ID, prior.ReviewID, prior.SessionID = "r-old", "rv1", "mer-1"
			if prior.Harness == "" {
				prior.Harness = domain.ReviewerClaudeCode
			}
			prior.PRURL, prior.CreatedAt = url, time.Now().Add(-time.Hour)
			if prior.TargetSHA == "" {
				prior.TargetSHA = "old"
			}
			prior.GithubReviewID = "posted-77"
			store := &fakeStore{runs: append([]domain.ReviewRun{prior}, tc.extra...)}
			launcher := &fakeLauncher{handle: "review-mer-1"}
			prs := fakePRs{prs: []domain.PullRequest{{URL: url, Number: 1, HeadSHA: "sha1", TargetBranch: "main"}}}
			cfg := domain.ProjectConfig{ApproveIdenticalPatch: tc.enabled}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{cfg: cfg}, launcher)
			eng.changedPaths = func(context.Context, string, domain.PullRequest) ([]string, error) { return nil, nil }
			eng.patchID = func(_ context.Context, _, _, sha string) (string, error) { return tc.patchIDs[sha], nil }
			if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
				t.Fatal(err)
			}
			var carried *domain.ReviewRun
			for i, r := range store.runs {
				if r.TargetSHA == "sha1" && r.Verdict == domain.VerdictApproved {
					carried = &store.runs[i]
				}
			}
			if (carried != nil) != tc.wantCarry || launcher.spawned == tc.wantCarry {
				t.Fatalf("carried=%v spawned=%v, want carry=%v", carried != nil, launcher.spawned, tc.wantCarry)
			}
			if tc.wantCarry && carried.DeliveredAt == nil {
				t.Fatal("a carried approval must not be delivered to the worker again")
			}
			if tc.wantCarry && (carried.GithubReviewID != prior.GithubReviewID || carried.NotPostedToPR()) {
				t.Fatalf("a carried approval must keep the posted review: id=%q notPosted=%v", carried.GithubReviewID, carried.NotPostedToPR())
			}
		})
	}
}

func TestGitPatchIDIgnoresRebase(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rev := func(ref string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out[:len(out)-1])
	}
	git("init", "-q", "-b", "main")
	write("a.txt", "a\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "feat")
	write("f.txt", "feature\n")
	git("add", "-A")
	git("commit", "-q", "-m", "feature")
	old := rev("HEAD")
	git("checkout", "-q", "main")
	write("b.txt", "b\n")
	git("add", "-A")
	git("commit", "-q", "-m", "main moves")
	git("checkout", "-q", "feat")
	git("rebase", "-q", "main")
	rebased := rev("HEAD")
	write("f.txt", "feature changed\n")
	git("commit", "-q", "-am", "edit")
	edited := rev("HEAD")
	ctx := context.Background()
	idOld, err1 := gitPatchID(ctx, dir, "main", old)
	idRebased, err2 := gitPatchID(ctx, dir, "main", rebased)
	idEdited, err3 := gitPatchID(ctx, dir, "main", edited)
	if err1 != nil || err2 != nil || err3 != nil || idOld == "" {
		t.Fatalf("errs %v %v %v id %q", err1, err2, err3, idOld)
	}
	if old == rebased {
		t.Fatal("rebase did not change the commit")
	}
	if idOld != idRebased || idOld == idEdited {
		t.Fatalf("patch ids: old=%s rebased=%s edited=%s", idOld, idRebased, idEdited)
	}
}
