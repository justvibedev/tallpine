package controllers_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func inboxCountsOf(t *testing.T, out map[string]any) map[string]int {
	t.Helper()
	got := map[string]int{}
	for id, n := range out["counts"].(map[string]any) {
		got[id] = int(n.(float64))
	}
	return got
}

// The sidebar number: inbox items per non-archived project. Open items,
// archived projects and empty inboxes do not count, and an orchestrator token
// only sees its own project.
func TestBacklogInboxCounts(t *testing.T) {
	a := newBacklogAPI(t)
	ctx := context.Background()
	if err := a.store.UpsertProject(ctx, domain.ProjectRecord{ID: "p3", Path: "/tmp/p3", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := a.store.UpsertProject(ctx, domain.ProjectRecord{ID: "p4", Path: "/tmp/p4", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// Workers always file to the inbox.
	a.must(201, "POST", "/projects/p1/backlog", "worker:s1", map[string]any{"title": "from worker one"})
	a.must(201, "POST", "/projects/p1/backlog", "worker:s1", map[string]any{"title": "from worker two"})
	a.must(201, "POST", "/projects/p2/backlog", "worker:s1", map[string]any{"title": "other project"})
	a.must(201, "POST", "/projects/p3/backlog", "worker:s1", map[string]any{"title": "soon archived"})
	// A human-filed open item is not waiting for triage; p4 has only that.
	a.must(201, "POST", "/projects/p1/backlog", "", map[string]any{"title": "my own idea"})
	a.must(201, "POST", "/projects/p4/backlog", "", map[string]any{"title": "my own idea"})
	if ok, err := a.store.ArchiveProject(ctx, "p3", time.Now().UTC()); err != nil || !ok {
		t.Fatalf("archive: %v %v", ok, err)
	}

	got := inboxCountsOf(t, a.must(200, "GET", "/backlog/inbox-counts", "", nil))
	if want := map[string]int{"p1": 2, "p2": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("human counts = %v, want %v", got, want)
	}

	got = inboxCountsOf(t, a.must(200, "GET", "/backlog/inbox-counts", "orchestrator:orch", nil))
	if want := map[string]int{"p1": 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("orchestrator counts = %v, want only its own project %v", got, want)
	}

	// Accepting an inbox item removes it from the count.
	a.must(200, "POST", "/projects/p1/backlog/B1/state", "", map[string]any{"state": "open"})
	got = inboxCountsOf(t, a.must(200, "GET", "/backlog/inbox-counts", "", nil))
	if want := map[string]int{"p1": 1, "p2": 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("after accept = %v, want %v", got, want)
	}
}

// A parent's child counts ride on every read, so the UI's "2/3 done" does not
// depend on how many closed children a list page happened to fetch.
func TestBacklogChildCountsOnListAndGet(t *testing.T) {
	a := newBacklogAPI(t)
	base := "/projects/p1/backlog"
	a.must(201, "POST", base, "", map[string]any{"title": "parent"})
	for _, title := range []string{"one", "two", "three"} {
		a.must(201, "POST", base, "", map[string]any{"title": title, "parent": "B1"})
	}
	a.must(200, "POST", base+"/B2/state", "", map[string]any{"state": "done"})
	a.must(200, "POST", base+"/B3/state", "", map[string]any{"state": "dropped"})

	counts := func(item map[string]any) [2]int {
		return [2]int{int(item["childCount"].(float64)), int(item["childClosedCount"].(float64))}
	}
	byRef := map[string][2]int{}
	for _, raw := range a.must(200, "GET", base, "", nil)["items"].([]any) {
		item := raw.(map[string]any)
		byRef[item["ref"].(string)] = counts(item)
	}
	if byRef["B1"] != [2]int{3, 2} || byRef["B2"] != [2]int{0, 0} || byRef["B4"] != [2]int{0, 0} {
		t.Fatalf("list counts = %v, want B1 {3 2} and no counts on children", byRef)
	}
	if got := counts(itemOf(a.must(200, "GET", base+"/B1", "", nil))); got != [2]int{3, 2} {
		t.Fatalf("single read counts = %v, want {3 2}", got)
	}
	// Reopening a child lowers the closed count.
	a.must(200, "POST", base+"/B2/state", "", map[string]any{"state": "open"})
	if got := counts(itemOf(a.must(200, "GET", base+"/B1", "", nil))); got != [2]int{3, 1} {
		t.Fatalf("after reopen = %v, want {3 1}", got)
	}
}
