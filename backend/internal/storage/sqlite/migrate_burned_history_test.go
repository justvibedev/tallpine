package sqlite

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// TestSessionListSucceedsOnBurnedMigrationHistory reproduces the #3475/#3476
// field profile: goose_db_version already records versions 40 through 51
// (written by a foreign build), so goose silently skips the real
// 0040_add_session_diff_base.sql and the sessions table lacks the diff-base
// columns. Startup schema reconciliation must repair the physical schema so
// the session list works instead of returning 500 INTERNAL_ERROR.
func TestSessionListSucceedsOnBurnedMigrationHistory(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 39) // the real 0040 has not run; diff-base columns are absent
	for v := 40; v <= 51; v++ {
		if _, err := db.Exec(
			`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, v,
		); err != nil {
			t.Fatalf("seed burned version %d: %v", v, err)
		}
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate burned profile: %v", err)
	}

	ctx := t.Context()
	store := sqlitestore.NewStore(db, db)
	if _, err := db.Exec(`
INSERT INTO projects (
	id, path, repo_origin_url, display_name, registered_at, config, kind
) VALUES (?, ?, ?, ?, ?, ?, ?)
`,
		"mer",
		`C:\src\mer`,
		"https://example.com/mer.git",
		"Mer",
		"2026-07-23T00:00:00Z",
		`{"worker":{"agent":"claude-code"}}`,
		"single_repo",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	rec := domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{
			Branch:        "tp/mer-1/root",
			WorkspacePath: `C:\Users\mer\.tallpine\data\worktrees\mer\mer-1`,
			DiffBaseSHA:   "0f0e0d0c0b0a09080706050403020100ffeeddcc",
			DiffBaseRef:   "refs/remotes/origin/main",
		},
	}
	created, err := store.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("create session on repaired schema: %v", err)
	}

	sessions, err := store.ListAllSessions(ctx)
	if err != nil {
		t.Fatalf("session list on burned profile: %v", err)
	}
	if len(sessions) != 1 || sessions[0].ID != created.ID {
		t.Fatalf("sessions = %+v, want the created session", sessions)
	}
	if sessions[0].Metadata.DiffBaseSHA != rec.Metadata.DiffBaseSHA || sessions[0].Metadata.DiffBaseRef != rec.Metadata.DiffBaseRef {
		t.Fatalf("diff base metadata = (%q, %q), want it round-tripped",
			sessions[0].Metadata.DiffBaseSHA, sessions[0].Metadata.DiffBaseRef)
	}
	now := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	if err := store.UpsertReview(ctx, domain.Review{
		ID:               "review-1",
		SessionID:        created.ID,
		ProjectID:        "mer",
		Harness:          domain.ReviewerCodex,
		ReviewerHandleID: "review-mer-1",
		AgentSessionID:   "reviewer-native-1",
		CreatedAt:        now,
		UpdatedAt:        now,
	}); err != nil {
		t.Fatalf("upsert review on repaired schema: %v", err)
	}
	review, ok, err := store.GetReviewBySessionAndHarness(ctx, created.ID, domain.ReviewerCodex)
	if err != nil {
		t.Fatalf("get review on repaired schema: %v", err)
	}
	if !ok || review.AgentSessionID != "reviewer-native-1" {
		t.Fatalf("review = %+v, ok=%v, want persisted reviewer native id", review, ok)
	}

	// The repair is idempotent: a second startup on the repaired database (and
	// on any healthy database) is a no-op, never a duplicate-column error.
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate on repaired schema: %v", err)
	}
	for table, want := range map[string][]string{
		"sessions":      {"diff_base_sha", "diff_base_ref", "reviewer_harness", "browser_capability_verifier", "auto_inject_review", "auto_inject_ci"},
		"pr":            {"auto_inject_ci"},
		"notifications": {"resolved_at"},
	} {
		for _, column := range want {
			var columns int
			if err := db.QueryRow(
				`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
			).Scan(&columns); err != nil {
				t.Fatal(err)
			}
			if columns != 1 {
				t.Fatalf("%s.%s count = %d, want exactly 1 after repair", table, column, columns)
			}
		}
	}
}
