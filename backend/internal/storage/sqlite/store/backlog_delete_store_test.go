package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func addClosedBacklog(t *testing.T, s *sqlite.Store, project, title string, state domain.BacklogState, parent string, closedAt time.Time) domain.BacklogItem {
	t.Helper()
	backlogSeq++
	item, err := s.CreateBacklogItem(context.Background(), domain.BacklogItem{
		ID: fmt.Sprintf("bl-%d", backlogSeq), ProjectID: domain.ProjectID(project), Title: title, State: state,
		ParentID: parent, Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman},
		CreatedAt: closedAt, UpdatedAt: closedAt, ClosedAt: &closedAt,
	})
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return item
}

func backlogRefs(items []domain.BacklogItem) []string {
	refs := []string{}
	for _, it := range items {
		refs = append(refs, it.Ref())
	}
	return refs
}

func backlogExists(t *testing.T, s *sqlite.Store, id string) bool {
	t.Helper()
	_, ok, err := s.GetBacklogItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// backlogDeleteEvents returns the refs of backlog_changed rows after seq, in order.
func backlogChangeRefs(t *testing.T, s *sqlite.Store, after int64) ([]string, int64) {
	t.Helper()
	rows, err := s.EventsAfter(context.Background(), after, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	last := after
	for _, e := range rows {
		last = e.Seq
		if e.Type != cdc.EventBacklogChanged {
			continue
		}
		var p map[string]string
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		refs = append(refs, p["ref"])
	}
	return refs, last
}

func TestDeleteBacklogItemRefusesOpenAndInbox(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	for _, st := range []domain.BacklogState{domain.BacklogOpen, domain.BacklogInbox} {
		it := addBacklog(t, s, "p1", string(st), st, "")
		_, err := s.DeleteBacklogItem(ctx, it.ID)
		if !errors.Is(err, domain.ErrBacklogNotClosed) {
			t.Fatalf("delete %s item: err = %v, want ErrBacklogNotClosed", st, err)
		}
		if !backlogExists(t, s, it.ID) {
			t.Fatalf("%s item vanished after a refused delete", st)
		}
	}
	if _, err := s.DeleteBacklogItem(ctx, "nope"); !errors.Is(err, domain.ErrBacklogNotFound) {
		t.Fatalf("unknown id: err = %v", err)
	}
}

func TestDeleteBacklogParentDeletesClosedChildrenOrRefuses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	parent := addClosedBacklog(t, s, "p1", "parent", domain.BacklogDone, "", backlogNow)
	done := addClosedBacklog(t, s, "p1", "kid-done", domain.BacklogDone, parent.ID, backlogNow)
	// A live child can only be filed under a live parent, so reopen the parent
	// around it: child open, then parent closed by hand.
	open := addBacklog(t, s, "p1", "kid-open", domain.BacklogOpen, parent.ID)
	if _, err := s.SetBacklogItemState(ctx, parent.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	_, last := backlogChangeRefs(t, s, 0)

	if _, err := s.DeleteBacklogItem(ctx, parent.ID); !errors.Is(err, domain.ErrBacklogNotClosed) {
		t.Fatalf("parent with open child: err = %v, want ErrBacklogNotClosed", err)
	}
	for _, id := range []string{parent.ID, done.ID, open.ID} {
		if !backlogExists(t, s, id) {
			t.Fatalf("%s deleted despite the refusal", id)
		}
	}
	if refs, _ := backlogChangeRefs(t, s, last); len(refs) != 0 {
		t.Fatalf("refused delete emitted CDC: %v", refs)
	}

	if _, err := s.SetBacklogItemState(ctx, open.ID, domain.BacklogDropped, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	_, last = backlogChangeRefs(t, s, 0)
	deleted, err := s.DeleteBacklogItem(ctx, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := backlogRefs(deleted); len(got) != 3 || got[2] != parent.Ref() {
		t.Fatalf("deleted = %v, want both children then the parent", got)
	}
	for _, id := range []string{parent.ID, done.ID, open.ID} {
		if backlogExists(t, s, id) {
			t.Fatalf("%s survived the delete", id)
		}
	}
	// One backlog_changed row per deleted item, children first.
	refs, _ := backlogChangeRefs(t, s, last)
	if len(refs) != 3 || refs[0] != done.Ref() || refs[1] != open.Ref() || refs[2] != parent.Ref() {
		t.Fatalf("CDC refs = %v, want one per delete", refs)
	}
}

func TestDeleteBacklogNeverReusesRefs(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	first := addClosedBacklog(t, s, "p1", "first", domain.BacklogDone, "", backlogNow)
	if _, err := s.DeleteBacklogItem(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	next := addBacklog(t, s, "p1", "next", domain.BacklogOpen, "")
	if next.Seq != first.Seq+1 {
		t.Fatalf("next seq = %d after deleting seq %d, want %d (refs are never reused)", next.Seq, first.Seq, first.Seq+1)
	}
}

func TestPurgeBacklogFiltersAndEmitsPerDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	seedProject(t, s, "p2")
	old := backlogNow.AddDate(0, 0, -40)
	oldDone := addClosedBacklog(t, s, "p1", "old done", domain.BacklogDone, "", old)
	newDone := addClosedBacklog(t, s, "p1", "new done", domain.BacklogDone, "", backlogNow)
	oldDrop := addClosedBacklog(t, s, "p1", "old dropped", domain.BacklogDropped, "", old)
	live := addBacklog(t, s, "p1", "live", domain.BacklogOpen, "")
	other := addClosedBacklog(t, s, "p2", "other project", domain.BacklogDone, "", old)
	cutoff := backlogNow.AddDate(0, 0, -30)
	_, last := backlogChangeRefs(t, s, 0)

	// Dry run reports and changes nothing.
	res, err := s.PurgeBacklog(ctx, store.BacklogPurgeInput{ProjectID: "p1", Done: true, Dropped: true, ClosedBefore: &cutoff, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := backlogRefs(res.Deleted); len(got) != 2 {
		t.Fatalf("dry run lists %v, want the two old closed items", got)
	}
	if refs, _ := backlogChangeRefs(t, s, last); len(refs) != 0 || !backlogExists(t, s, oldDone.ID) {
		t.Fatalf("dry run changed state: cdc=%v", refs)
	}

	// --done only, older than 30 days: just the old done item.
	res, err = s.PurgeBacklog(ctx, store.BacklogPurgeInput{ProjectID: "p1", Done: true, ClosedBefore: &cutoff})
	if err != nil {
		t.Fatal(err)
	}
	if got := backlogRefs(res.Deleted); len(got) != 1 || got[0] != oldDone.Ref() {
		t.Fatalf("deleted = %v, want only %s", got, oldDone.Ref())
	}
	if refs, _ := backlogChangeRefs(t, s, last); len(refs) != 1 || refs[0] != oldDone.Ref() {
		t.Fatalf("CDC refs = %v, want one row for %s", refs, oldDone.Ref())
	}
	for id, want := range map[string]bool{oldDone.ID: false, newDone.ID: true, oldDrop.ID: true, live.ID: true, other.ID: true} {
		if backlogExists(t, s, id) != want {
			t.Fatalf("%s exists = %v, want %v", id, !want, want)
		}
	}

	// Everything closed in p1, any age: the rest of the closed items, never the open one or another project's.
	res, err = s.PurgeBacklog(ctx, store.BacklogPurgeInput{ProjectID: "p1", Done: true, Dropped: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := backlogRefs(res.Deleted); len(got) != 2 {
		t.Fatalf("deleted = %v, want newDone and oldDrop", got)
	}
	if !backlogExists(t, s, live.ID) || !backlogExists(t, s, other.ID) {
		t.Fatal("purge reached an open item or another project")
	}
}

func TestPurgeBacklogSkipsParentWithOpenChild(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	parent := addClosedBacklog(t, s, "p1", "parent", domain.BacklogDone, "", backlogNow)
	kid := addBacklog(t, s, "p1", "open kid", domain.BacklogOpen, parent.ID)
	if _, err := s.SetBacklogItemState(ctx, parent.ID, domain.BacklogDone, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	res, err := s.PurgeBacklog(ctx, store.BacklogPurgeInput{ProjectID: "p1", Done: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || len(res.Skipped) != 1 || res.Skipped[0].ID != parent.ID {
		t.Fatalf("result = %+v, want the parent skipped", res)
	}
	if !backlogExists(t, s, parent.ID) || !backlogExists(t, s, kid.ID) {
		t.Fatal("parent or its open child was deleted")
	}
}

// A child re-parented under a later parent has a lower seq than it; a dry run
// must list it once, exactly as the real run deletes it.
func TestPurgeBacklogDryRunListsReparentedChildOnce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")
	child := addClosedBacklog(t, s, "p1", "child", domain.BacklogDone, "", backlogNow)
	parent := addClosedBacklog(t, s, "p1", "parent", domain.BacklogDone, "", backlogNow)
	if _, err := s.UpdateBacklogItem(ctx, child.ID, backlogNow, func(i *domain.BacklogItem) error { i.ParentID = parent.ID; return nil }); err != nil {
		t.Fatal(err)
	}
	in := store.BacklogPurgeInput{ProjectID: "p1", Done: true}
	in.DryRun = true
	dry, err := s.PurgeBacklog(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.DryRun = false
	applied, err := s.PurgeBacklog(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	d, r := backlogRefs(dry.Deleted), backlogRefs(applied.Deleted)
	if len(d) != 2 || fmt.Sprint(d) != fmt.Sprint(r) {
		t.Fatalf("dry run = %v, real run = %v, want the same two refs", d, r)
	}
}
