package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

// queryPlan returns EXPLAIN QUERY PLAN detail lines for query.
func queryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

// Break caught: settling a dead controller's work on every Chat start
// full-scanned conversation_turns and conversation_activities on the writer,
// seconds on a long history. Mirrors FailOrphanedConversationActivities and
// SettleOrphanedConversationTurns in queries/conversations.sql.
func TestOrphanSettleUsesIndexes(t *testing.T) {
	db := openMigratedTestDB(t)

	activities := queryPlan(t, db, `UPDATE conversation_activities
SET status = 'failed', revision = revision + 1, updated_at = ?
WHERE status = 'running'
  AND turn_id IN (
      SELECT id FROM conversation_turns
      WHERE handled_by_session_id = ?
        AND state IN ('queued', 'running')
  )`, "now", "s-1")
	for _, want := range []string{"INDEX idx_conversation_activities_running_turn", "INDEX idx_conversation_turns_handled_by_session"} {
		if !strings.Contains(activities, want) {
			t.Fatalf("orphaned activity settle does not use %s:\n%s", want, activities)
		}
	}
	if strings.Contains(activities, "SCAN ") {
		t.Fatalf("orphaned activity settle scans a table:\n%s", activities)
	}

	turns := queryPlan(t, db, `UPDATE conversation_turns
SET state = 'failed', error_message = 'x', completed_at = ?
WHERE handled_by_session_id = ? AND state IN ('queued', 'running')`, "now", "s-1")
	if !strings.Contains(turns, "INDEX idx_conversation_turns_handled_by_session") || strings.Contains(turns, "SCAN ") {
		t.Fatalf("orphaned turn settle does not use idx_conversation_turns_handled_by_session:\n%s", turns)
	}
}
