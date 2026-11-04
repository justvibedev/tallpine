package sqlite

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// Deleting a project must cascade through a parent/child backlog pair; with
// foreign keys on, a RESTRICT self-reference on parent_id made the delete fail.
func TestBacklogProjectDeleteCascadesParentAndChild(t *testing.T) {
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
	mk := func(id, parent string) {
		if _, err := s.CreateBacklogItem(ctx, domain.BacklogItem{
			ID: id, ProjectID: "p1", Title: id, State: domain.BacklogOpen, ParentID: parent,
			Origin: domain.BacklogOrigin{Kind: domain.BacklogOriginHuman}, CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("parent", "")
	mk("child", "parent")

	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	// change_log.project_id references projects (as for session events), so a
	// project's CDC rows go first; backlog writes emit backlog_changed rows.
	if _, err := db.Exec(`DELETE FROM change_log WHERE project_id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM projects WHERE id = 'p1'`); err != nil {
		t.Fatalf("project delete blocked by backlog hierarchy: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM backlog_items`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("backlog items left after project delete: n=%d err=%v", n, err)
	}
}
