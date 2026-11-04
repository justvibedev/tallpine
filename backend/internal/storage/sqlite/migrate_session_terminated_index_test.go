package sqlite

import (
	"strings"
	"testing"
)

// Break caught: the session list the app polls (?active=true) decoded every
// session ever recorded to return the few live ones. The live read and the
// archive count must both stay on idx_sessions_terminated; the count must not
// touch the wide session rows at all.
func TestSessionListReadsUseTerminatedIndex(t *testing.T) {
	db := openMigratedTestDB(t)
	plan := func(query string) string {
		t.Helper()
		rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var lines []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}

	live := plan(`SELECT id FROM sessions WHERE is_terminated = 0 ORDER BY project_id, num`)
	if !strings.Contains(live, "INDEX idx_sessions_terminated (is_terminated=?)") {
		t.Fatalf("live session list does not seek idx_sessions_terminated:\n%s", live)
	}
	archived := plan(`SELECT project_id, kind, COUNT(*) AS sessions FROM sessions
WHERE is_terminated = 1 AND is_task_preparation = 0 GROUP BY project_id, kind`)
	if !strings.Contains(archived, "COVERING INDEX idx_sessions_terminated") {
		t.Fatalf("archive count is not answered from idx_sessions_terminated alone:\n%s", archived)
	}
}
