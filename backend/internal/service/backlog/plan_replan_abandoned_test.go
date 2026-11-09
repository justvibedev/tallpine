package backlog_test

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestReplan_RetiresUnscopedChildUnderPRRunWhoseWorkersAllEndedWithoutPR(t *testing.T) {
	e := approvedWith(t, "A", "B")
	kids := e.children()
	rec := e.workerSession()
	if err := e.bl.LinkSession(e.ctx, kids[0].ID, rec.ID, "human"); err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := e.store.UpdateSession(e.ctx, rec); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := e.store.CreateBacklogRun(e.ctx, domain.BacklogRun{
		ID: "run-1", ProjectID: "p1", Scope: domain.BacklogScopePR, SelectionKind: domain.BacklogRunSelectAll,
		Status: domain.BacklogRunRunning, RequestedBy: domain.BacklogOriginHuman, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	e.replanTo(prs("A2", "B2"))
	if got := e.liveTitles(); !eq(got, "A2#1", "B2#2") {
		t.Fatalf("live children = %v, want only the new split", got)
	}
}

// workerSession makes a live worker session record.
func (e *planEnv) workerSession() domain.SessionRecord {
	e.t.Helper()
	now := time.Now().UTC()
	rec, err := e.store.CreateSession(e.ctx, domain.SessionRecord{
		ProjectID: "p1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		Metadata: domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return rec
}

func TestReplan_KeepsLocalScopeChildWhoseWorkerEnded(t *testing.T) {
	e := approvedWith(t, "A", "B")
	kids := e.children()
	rec := e.workerSession()
	if err := e.bl.LinkSession(e.ctx, kids[0].ID, rec.ID, "human"); err != nil {
		t.Fatal(err)
	}
	rec.IsTerminated = true
	if err := e.store.UpdateSession(e.ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.UpdateBacklogItem(e.ctx, kids[0].ID, time.Now().UTC(), func(i *domain.BacklogItem) error {
		i.Scope = domain.BacklogScopeLocal
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	e.replanTo(prs("A2"))
	v, _ := e.bl.Get(e.ctx, "p1", kids[0].Ref())
	if v.State != domain.BacklogOpen {
		t.Fatalf("local child = %s, want kept open", v.State)
	}
}
