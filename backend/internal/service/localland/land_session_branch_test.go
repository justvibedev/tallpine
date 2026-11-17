package localland

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/review"
)

// B748: a worker recorded on tp/<id>/root that committed on a self-made
// tp/<id>/<topic> branch lands that branch, matching the review key it was
// approved under.
func TestLandFollowsSelfMadeSessionBranch(t *testing.T) {
	f := newFixture(t)
	wt := filepath.Join(filepath.Dir(f.repo), "w1")
	run(t, f.repo, "worktree", "add", "-q", "-b", "tp/w1/root", wt, "main")
	run(t, wt, "switch", "-q", "-c", "tp/w1/topic", "--no-track", "tp/w1/root")
	head := commitFile(t, wt, "topic.txt", "topic\n", "topic work")
	id := domain.SessionID("w1")
	f.store.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "p1", DisplayName: "Land w1",
		Metadata: domain.SessionMetadata{Branch: "tp/w1/root", WorkspacePath: wt, DiffBaseRef: "main"}}
	f.store.runs[id] = []domain.ReviewRun{{ID: "r1", SessionID: id, PRURL: review.LocalTargetURL("tp/w1/topic"),
		TargetSHA: head, Verdict: domain.VerdictApproved, CreatedAt: time.Now()}}

	res, err := f.svc.Land(context.Background(), id, Request{})
	if err != nil {
		t.Fatalf("Land: %v", err)
	}
	if got := run(t, f.repo, "rev-parse", "main"); got != res.NewSHA || res.NewSHA == "" {
		t.Fatalf("main = %s, result %+v", got, res)
	}
	if got := run(t, f.repo, "show", "main:topic.txt"); got != "topic" {
		t.Fatalf("landed tree lacks topic.txt: %q", got)
	}
}
