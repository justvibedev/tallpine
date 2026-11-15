package backlog_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/backlog"
)

// skipDiff models a default branch where the sibling's merge commit "sib"
// changed ui/seat.tsx and anything else in foreign changed on its own.
type skipDiff struct{ foreign []string }

func (d *skipDiff) ChangedSince(context.Context, domain.ProjectID, string) ([]string, error) {
	return append([]string{"ui/seat.tsx"}, d.foreign...), nil
}

func (d *skipDiff) ChangedSinceSkipping(_ context.Context, _ domain.ProjectID, _ string, skip []string) ([]string, error) {
	for _, s := range skip {
		if s == "sib" {
			return d.foreign, nil
		}
	}
	return d.ChangedSince(context.Background(), "", "")
}

func (d *skipDiff) ChangedLinesSkipping(context.Context, domain.ProjectID, string, []string, []string) (map[string][]string, error) {
	return nil, nil
}

// B737: an earlier sibling's merge touching a file this child also names is the
// plan working, not drift; a commit that is not the sibling's still is.
func TestStale_SkipsMergedSiblingCommitOnly(t *testing.T) {
	diff := &skipDiff{}
	e := newPlanEnvWith(t, true, func(d *backlog.PlanDeps) { d.Diff = diff })
	p := e.approved(twoPRSubmit())
	e.setBase(p.ID, "abc123")
	kids := e.children()
	url := "https://github.com/o/r/pull/7"
	now := time.Now().UTC()
	rec, err := e.store.CreateSession(e.ctx, domain.SessionRecord{
		ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := domain.PullRequest{SessionID: rec.ID, URL: url, Number: 7, Merged: true, MergeCommitSHA: "sib", UpdatedAt: time.Now().UTC()}
	if err := e.store.WritePR(e.ctx, pr, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkPRMerged(e.ctx, url, "sib", now); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bl.Link(e.ctx, "p1", kids[0].ID, domain.BacklogLinkPR, url, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bl.SetState(e.ctx, "p1", kids[0].ID, domain.BacklogDone, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false); err != nil {
		t.Fatalf("only the sibling's merge changed the file = %v, want startable", err)
	}
	diff.foreign = []string{"ui/seat.tsx"} // someone else's commit in the same window
	if _, err := e.bl.PrepareSpawn(e.ctx, "p1", kids[1].Ref(), false); apiCode(t, err) != "PLAN_STALE" {
		t.Fatalf("foreign change to the child's file = %v, want PLAN_STALE", err)
	}
}
