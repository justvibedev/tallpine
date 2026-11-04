package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func stateOf(t *testing.T, id string, s interface {
	GetBacklogItem(context.Context, string) (domain.BacklogItem, bool, error)
}) domain.BacklogItem {
	t.Helper()
	it, ok, err := s.GetBacklogItem(context.Background(), id)
	if err != nil || !ok {
		t.Fatalf("get %s: ok=%v err=%v", id, ok, err)
	}
	return it
}

func TestBacklogParentClosesWhenAllChildrenClosed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	parent := addBacklog(t, s, "p1", "parent", domain.BacklogOpen, "")
	a := addBacklog(t, s, "p1", "a", domain.BacklogOpen, parent.ID)
	b := addBacklog(t, s, "p1", "b", domain.BacklogOpen, parent.ID)

	if _, err := s.SetBacklogItemState(ctx, a.ID, domain.BacklogDone, "x", backlogNow); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogOpen {
		t.Fatalf("parent with an open child = %s, want open", p.State)
	}
	if _, err := s.SetBacklogItemState(ctx, b.ID, domain.BacklogDropped, "no", backlogNow); err != nil {
		t.Fatal(err)
	}
	p := stateOf(t, parent.ID, s)
	if p.State != domain.BacklogDone || p.ClosedReason != "children closed" || p.ClosedAt == nil {
		t.Fatalf("parent = %+v, want done/children closed (dropped counts as closed)", p)
	}

	// Reopening a child reopens a parent closed this way.
	if _, err := s.SetBacklogItemState(ctx, b.ID, domain.BacklogOpen, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	p = stateOf(t, parent.ID, s)
	if p.State != domain.BacklogOpen || p.ClosedReason != "" || p.ClosedAt != nil {
		t.Fatalf("parent after child reopen = %+v", p)
	}
}

func TestBacklogManuallyClosedParentIsNeverAutoReopened(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	parent := addBacklog(t, s, "p1", "parent", domain.BacklogOpen, "")
	child := addBacklog(t, s, "p1", "child", domain.BacklogOpen, parent.ID)
	if _, err := s.SetBacklogItemState(ctx, parent.ID, domain.BacklogDone, "shipped by hand", backlogNow); err != nil {
		t.Fatal(err)
	}
	// A child changing state leaves a manually closed parent alone, both ways.
	if _, err := s.SetBacklogItemState(ctx, child.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetBacklogItemState(ctx, child.ID, domain.BacklogOpen, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogDone || p.ClosedReason != "shipped by hand" {
		t.Fatalf("manually closed parent = %+v", p)
	}
	// Adding a live child under it does not reopen it either.
	addBacklog(t, s, "p1", "late child", domain.BacklogOpen, parent.ID)
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogDone {
		t.Fatalf("parent reopened by a new child: %+v", p)
	}
}

func TestBacklogLiveChildAddedOrReparentedReopensAutoClosedParent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	parent := addBacklog(t, s, "p1", "parent", domain.BacklogOpen, "")
	only := addBacklog(t, s, "p1", "only", domain.BacklogOpen, parent.ID)
	if _, err := s.SetBacklogItemState(ctx, only.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogDone {
		t.Fatalf("setup: parent = %+v", p)
	}

	// A new live child reopens it.
	added := addBacklog(t, s, "p1", "added", domain.BacklogInbox, parent.ID)
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogOpen {
		t.Fatalf("after add: parent = %+v", p)
	}
	// Close both again, then re-parent a live loose item under it.
	if _, err := s.SetBacklogItemState(ctx, added.ID, domain.BacklogDropped, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogDone {
		t.Fatalf("setup 2: parent = %+v", p)
	}
	loose := addBacklog(t, s, "p1", "loose", domain.BacklogOpen, "")
	if _, err := s.UpdateBacklogItem(ctx, loose.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = parent.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, parent.ID, s); p.State != domain.BacklogOpen {
		t.Fatalf("after reparent: parent = %+v", p)
	}
}

func TestBacklogReparentingAwayTheLastOpenChildDoesNotRerunClosure(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	p1 := addBacklog(t, s, "p1", "p1", domain.BacklogOpen, "")
	p2 := addBacklog(t, s, "p1", "p2", domain.BacklogOpen, "")
	done := addBacklog(t, s, "p1", "done child", domain.BacklogOpen, p1.ID)
	live := addBacklog(t, s, "p1", "live child", domain.BacklogOpen, p1.ID)
	if _, err := s.SetBacklogItemState(ctx, done.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateBacklogItem(ctx, live.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = p2.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	if p := stateOf(t, p1.ID, s); p.State != domain.BacklogOpen {
		t.Fatalf("old parent = %+v: closure must not re-run when a child is moved away", p)
	}
}

func TestCloseBacklogItemIfLiveNeverOverwritesAClosedItem(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	it := addBacklog(t, s, "p1", "x", domain.BacklogOpen, "")
	if _, err := s.SetBacklogItemState(ctx, it.ID, domain.BacklogDropped, "manual", backlogNow); err != nil {
		t.Fatal(err)
	}
	got, changed, err := s.CloseBacklogItemIfLive(ctx, it.ID, domain.BacklogDone, "merged #1", backlogNow)
	if err != nil || changed || got.State != domain.BacklogDropped || got.ClosedReason != "manual" {
		t.Fatalf("got=%+v changed=%v err=%v", got, changed, err)
	}
	live := addBacklog(t, s, "p1", "y", domain.BacklogOpen, "")
	got, changed, err = s.CloseBacklogItemIfLive(ctx, live.ID, domain.BacklogDone, "merged #2", backlogNow)
	if err != nil || !changed || got.State != domain.BacklogDone || got.ClosedReason != "merged #2" {
		t.Fatalf("got=%+v changed=%v err=%v", got, changed, err)
	}
}

func TestListExpiredBacklogInboxExcludesHumanAndFreshItems(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	mk := func(id string, kind domain.BacklogOriginKind, state domain.BacklogState, at time.Time) {
		var closed *time.Time
		if state.Closed() {
			closed = &at
		}
		if _, err := s.CreateBacklogItem(ctx, domain.BacklogItem{
			ID: id, ProjectID: "p1", Title: id, State: state, Origin: domain.BacklogOrigin{Kind: kind},
			CreatedAt: at, UpdatedAt: at, ClosedAt: closed,
		}); err != nil {
			t.Fatal(err)
		}
	}
	old := backlogNow.Add(-30 * 24 * time.Hour)
	mk("worker-old", domain.BacklogOriginWorker, domain.BacklogInbox, old)
	mk("orch-old", domain.BacklogOriginOrchestrator, domain.BacklogInbox, old)
	mk("reviewer-old", domain.BacklogOriginReviewer, domain.BacklogInbox, old)
	mk("human-old", domain.BacklogOriginHuman, domain.BacklogInbox, old)
	mk("worker-open-old", domain.BacklogOriginWorker, domain.BacklogOpen, old)
	mk("worker-fresh", domain.BacklogOriginWorker, domain.BacklogInbox, backlogNow.Add(-time.Hour))
	// Filed long ago but touched since (e.g. a link or a re-filed duplicate): not expired.
	if _, err := s.CreateBacklogItem(ctx, domain.BacklogItem{
		ID: "worker-touched", ProjectID: "p1", Title: "touched", State: domain.BacklogInbox,
		Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginWorker}, CreatedAt: old, UpdatedAt: backlogNow.Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListExpiredBacklogInbox(ctx, backlogNow.Add(-14*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, it := range got {
		ids[it.ID] = true
	}
	if len(got) != 3 || !ids["worker-old"] || !ids["orch-old"] || !ids["reviewer-old"] {
		t.Fatalf("expired = %v", ids)
	}
}

func TestExpireBacklogInboxItemRechecksInsideTheWriteTransaction(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	old := backlogNow.Add(-30 * 24 * time.Hour)
	cutoff := backlogNow.Add(-14 * 24 * time.Hour)
	mk := func(id string, kind domain.BacklogOriginKind) domain.BacklogItem {
		it, err := s.CreateBacklogItem(ctx, domain.BacklogItem{
			ID: id, ProjectID: "p1", Title: id, State: domain.BacklogInbox, Origin: domain.BacklogOrigin{Kind: kind}, CreatedAt: old, UpdatedAt: old,
		})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	stale := mk("stale", domain.BacklogOriginWorker)
	accepted := mk("accepted", domain.BacklogOriginWorker)
	edited := mk("edited", domain.BacklogOriginWorker)
	human := mk("human", domain.BacklogOriginHuman)

	// Triage lands after the expiry listing: accept one, edit another.
	if _, err := s.SetBacklogItemState(ctx, accepted.ID, domain.BacklogOpen, "", old); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateBacklogItem(ctx, edited.ID, backlogNow, func(i *domain.BacklogItem) error { i.Note = "still relevant"; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, it := range []domain.BacklogItem{accepted, edited, human} {
		got, changed, err := s.ExpireBacklogInboxItem(ctx, it.ID, cutoff, "expired", backlogNow)
		if err != nil || changed {
			t.Fatalf("%s: changed=%v err=%v, must be left alone", it.ID, changed, err)
		}
		if cur := stateOf(t, it.ID, s); cur.State == domain.BacklogDropped || got.State == domain.BacklogDropped {
			t.Fatalf("%s was dropped", it.ID)
		}
	}
	got, changed, err := s.ExpireBacklogInboxItem(ctx, stale.ID, cutoff, "expired", backlogNow)
	if err != nil || !changed || got.State != domain.BacklogDropped || got.ClosedReason != "expired" {
		t.Fatalf("stale: %+v changed=%v err=%v", got, changed, err)
	}
}
