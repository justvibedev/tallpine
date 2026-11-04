package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A hard delete must drop the item's links with it, and must not break a later
// project delete: backlog deletes write their own backlog_changed rows from the
// store (an AFTER DELETE trigger would fire during the project cascade and
// violate change_log's project foreign key).
func TestBacklogDeleteCascadesLinksAndProjectDeleteStillWorks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: "/tmp/p1", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	mk := func(id string, state domain.BacklogState) {
		in := domain.BacklogItem{ID: id, ProjectID: "p1", Title: id, State: state,
			Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: now, UpdatedAt: now}
		if state.Closed() {
			in.ClosedAt = &now
		}
		if _, err := s.CreateBacklogItem(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	mk("gone", domain.BacklogDone)
	mk("stays", domain.BacklogOpen)
	for _, id := range []string{"gone", "stays"} {
		if _, err := s.AddBacklogLink(ctx, id, domain.BacklogLink{Kind: domain.BacklogLinkSession, Ref: "s-" + id}, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.DeleteBacklogItem(ctx, "gone"); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	count := func(q string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(`SELECT count(*) FROM backlog_links WHERE item_id = 'gone'`); n != 0 {
		t.Fatalf("%d links of the deleted item survived", n)
	}
	if n := count(`SELECT count(*) FROM backlog_links WHERE item_id = 'stays'`); n != 1 {
		t.Fatalf("the other item's link count = %d, want 1", n)
	}
	if n := count(`SELECT next_seq FROM backlog_counters WHERE project_id = 'p1'`); n != 3 {
		t.Fatalf("next_seq = %d, want 3 (counter untouched by the delete)", n)
	}

	if _, err := db.Exec(`DELETE FROM change_log WHERE project_id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id = 'p1'`); err != nil {
		t.Fatalf("project delete failed after a backlog delete: %v", err)
	}
	if n := count(`SELECT count(*) FROM backlog_items`); n != 0 {
		t.Fatalf("%d backlog items left after the project delete", n)
	}
}
