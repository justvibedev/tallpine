package store_test

import (
	"context"
	"encoding/json"
	"testing"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Every backlog write path emits exactly one project-level backlog_changed
// event {id, ref, state}: create, field update, state change, and link
// add/remove (which bump updated_at, so the update trigger covers them). A
// duplicate link is a no-op and must stay silent.
func TestBacklogChangesEmitOneCDCEvent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "p1")

	var last int64
	next := func(step string) cdc.Event {
		t.Helper()
		rows, err := s.EventsAfter(ctx, last, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: %d change_log rows, want exactly 1: %+v", step, len(rows), rows)
		}
		last = rows[0].Seq
		e := rows[0]
		if e.Type != cdc.EventBacklogChanged || e.ProjectID != "p1" || e.SessionID != "" {
			t.Fatalf("%s: event = %+v, want project-level backlog_changed for p1", step, e)
		}
		return e
	}
	none := func(step string) {
		t.Helper()
		rows, err := s.EventsAfter(ctx, last, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 0 {
			t.Fatalf("%s: %d unexpected change_log rows: %+v", step, len(rows), rows)
		}
	}
	payload := func(e cdc.Event) map[string]string {
		t.Helper()
		var p map[string]string
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}

	item := addBacklog(t, s, "p1", "first", domain.BacklogInbox, "")
	p := payload(next("create"))
	if len(p) != 3 || p["id"] != item.ID || p["ref"] != item.Ref() || p["ref"] != domain.FormatBacklogRef(item.Seq) || p["state"] != "inbox" {
		t.Fatalf("create payload = %v (item %s %s)", p, item.ID, item.Ref())
	}

	if _, err := s.UpdateBacklogItem(ctx, item.ID, backlogNow, func(i *domain.BacklogItem) error { i.Note = "n"; return nil }); err != nil {
		t.Fatal(err)
	}
	if p := payload(next("update")); p["id"] != item.ID || p["state"] != "inbox" {
		t.Fatalf("update payload = %v", p)
	}

	if _, err := s.SetBacklogItemState(ctx, item.ID, domain.BacklogOpen, "", backlogNow); err != nil {
		t.Fatal(err)
	}
	if p := payload(next("state")); p["state"] != "open" {
		t.Fatalf("state payload = %v", p)
	}

	link := domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: "s1"}
	if added, err := s.AddBacklogLink(ctx, item.ID, link, backlogNow); err != nil || !added {
		t.Fatalf("add link: %v %v", added, err)
	}
	next("link add")
	if added, err := s.AddBacklogLink(ctx, item.ID, link, backlogNow); err != nil || added {
		t.Fatalf("duplicate link: %v %v", added, err)
	}
	none("duplicate link")
	if removed, err := s.RemoveBacklogLink(ctx, item.ID, link.Kind, link.Ref, backlogNow); err != nil || !removed {
		t.Fatalf("remove link: %v %v", removed, err)
	}
	next("link remove")

	// A deferred line that merges into a live item links the filing session.
	n := 0
	in := func(session, line string) store.BacklogDeferInput {
		return store.BacklogDeferInput{ProjectID: "p1", SessionID: session, Lines: []string{line}, Excerpt: "e", Now: backlogNow,
			NewID: func() string { n++; return "d-cdc" }}
	}
	if _, err := s.FileDeferredBacklog(ctx, in("s1", "deferred thing")); err != nil {
		t.Fatal(err)
	}
	next("defer create")
	if _, err := s.FileDeferredBacklog(ctx, in("s2", "deferred thing")); err != nil {
		t.Fatal(err)
	}
	next("defer merge")
}
