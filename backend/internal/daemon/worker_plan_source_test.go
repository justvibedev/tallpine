package daemon

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// The reviewer's plan comes from the approved plan of the worker's linked
// item, or that item's parent; never from an open, failed or missing plan.
func TestWorkerPlanSource_ApprovedPlanForWorker(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	newItem := func(id, parent string) domain.BacklogItem {
		t.Helper()
		it, err := store.CreateBacklogItem(ctx, domain.BacklogItem{
			ID: id, ProjectID: "mer", Title: id, State: domain.BacklogOpen, Scope: domain.BacklogScopeLocal, ParentID: parent,
			Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	link := func(it domain.BacklogItem, session string) {
		t.Helper()
		if _, err := store.AddBacklogLink(ctx, it.ID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: session, CreatedBy: "t"}, now); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(it domain.BacklogItem, status domain.PlanStatus, decisions string) {
		t.Helper()
		p := domain.BacklogPlan{ID: "plan-" + it.ID, ProjectID: "mer", ItemID: it.ID, Status: domain.PlanDrafting, StartedAt: now, UpdatedAt: now}
		if _, _, err := store.CreateBacklogPlan(ctx, p); err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateBacklogPlan(ctx, p.ID, func(pl *domain.BacklogPlan) error {
			pl.Status, pl.DecisionsMD, pl.ReviewFocus = status, decisions, []string{"focus " + it.ID}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	src := workerPlanSource{store: store}
	get := func(session string) (string, []string, bool) {
		t.Helper()
		d, f, ok, err := src.ApprovedPlanForWorker(ctx, domain.SessionID(session))
		if err != nil {
			t.Fatal(err)
		}
		return d, f, ok
	}

	own := newItem("own", "")
	link(own, "w-own")
	plan(own, domain.PlanApproved, "own decisions")
	if d, f, ok := get("w-own"); !ok || d != "own decisions" || len(f) != 1 || f[0] != "focus own" {
		t.Errorf("approved own plan: %q %v %v", d, f, ok)
	}

	parent := newItem("parent", "")
	child := newItem("child", "parent")
	link(child, "w-child")
	plan(parent, domain.PlanApproved, "parent decisions")
	if d, _, ok := get("w-child"); !ok || d != "parent decisions" {
		t.Errorf("parent fallback: %q %v", d, ok)
	}

	open := newItem("open", "")
	link(open, "w-open")
	plan(open, domain.PlanAwaitingApproval, "unapproved")
	if _, _, ok := get("w-open"); ok {
		t.Error("an unapproved plan must not reach the reviewer")
	}
	if _, _, ok := get("w-nobody"); ok {
		t.Error("a worker with no linked item has no plan")
	}
}

// The reviewer reads the current text of the item the worker was started on:
// an edit after the worker started is visible, and an item the worker filed
// itself is never picked, even when its id sorts first (B601).
func TestWorkerPlanSource_LinkedItemForWorkerIsCurrent(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Second)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	mk := func(id, doneWhen, createdBy string) domain.BacklogItem {
		t.Helper()
		it, err := store.CreateBacklogItem(ctx, domain.BacklogItem{
			ID: id, ProjectID: "mer", Title: id, DoneWhen: doneWhen, State: domain.BacklogOpen, Scope: domain.BacklogScopeLocal,
			Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddBacklogLink(ctx, it.ID, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: "w1", CreatedBy: createdBy}, now); err != nil {
			t.Fatal(err)
		}
		return it
	}
	src := workerPlanSource{store: store}
	if _, ok, err := src.LinkedItemForWorker(ctx, "other"); err != nil || ok {
		t.Fatalf("unlinked worker: ok=%v err=%v", ok, err)
	}
	spawned := mk("b-spawned", "old", "human")
	mk("a-deferred", "followup", "w1") // sorts before the spawned item
	if _, err := store.UpdateBacklogItem(ctx, spawned.ID, now, func(i *domain.BacklogItem) error { i.DoneWhen = "new"; return nil }); err != nil {
		t.Fatal(err)
	}
	got, ok, err := src.LinkedItemForWorker(ctx, "w1")
	if err != nil || !ok || got.ID != "b-spawned" || got.DoneWhen != "new" {
		t.Fatalf("got %+v ok=%v err=%v", got, ok, err)
	}
	mk("c-second", "other", "human") // ambiguous: two spawn-linked items
	if _, ok, err := src.LinkedItemForWorker(ctx, "w1"); err != nil || ok {
		t.Fatalf("ambiguous: ok=%v err=%v", ok, err)
	}
}
