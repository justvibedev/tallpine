package sqlite

import (
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	beforeLaneLifecycle = int64(20261030120000000)
	laneLifecycle       = int64(20261030130000000)
)

// seedLaneSession inserts a session and, when lane or parent is set, its
// session_lane row.
func seedLaneSession(t *testing.T, db *sql.DB, id string, num int, kind string, terminated bool, lane, parent string) {
	t.Helper()
	at := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
		VALUES (?, 'p1', ?, ?, 'idle', ?, ?, ?, ?)`, id, num, kind, at, terminated, at, at); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
	if lane == "" && parent == "" {
		return
	}
	if _, err := db.Exec(`INSERT INTO session_lane (session_id, lane_item_id, parent_session_id) VALUES (?, ?, ?)`, id, lane, parent); err != nil {
		t.Fatalf("seed lane of %s: %v", id, err)
	}
}

func seedLaneReport(t *testing.T, db *sql.DB, id, session, lane string) {
	t.Helper()
	at := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO reports (id, session_id, project_id, message, created_at, available_at, lane_item_id)
		VALUES (?, ?, 'p1', 'hi', ?, ?, ?)`, id, session, at, at, lane); err != nil {
		t.Fatalf("seed report %s: %v", id, err)
	}
}

// laneRows lists session_lane as "session:lane:parent", sorted.
func laneRows(t *testing.T, db *sql.DB) string {
	t.Helper()
	return queryStrings(t, db, `SELECT session_id || ':' || lane_item_id || ':' || parent_session_id FROM session_lane`)
}

func queryStrings(t *testing.T, db *sql.DB, q string) string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

// seedB108 is the shape left behind by the B108 smoke test: the lane's
// orchestrator (283) and its sessions (284, 392) all terminated, their
// session_lane rows still in place. Lane L200 is live and must survive.
func seedB108(t *testing.T, db *sql.DB) {
	t.Helper()
	at := time.Date(2026, 10, 29, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('p1', '/tmp/p1', 'proj', ?)`, at); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedLaneSession(t, db, "p1-1", 1, "orchestrator", false, "", "")
	seedLaneSession(t, db, "p1-283", 283, "orchestrator", true, "L108", "")
	seedLaneSession(t, db, "p1-284", 284, "worker", true, "L108", "p1-283")
	seedLaneSession(t, db, "p1-392", 392, "worker", true, "L108", "p1-283")
	seedLaneSession(t, db, "p1-500", 500, "orchestrator", false, "L200", "")
	seedLaneSession(t, db, "p1-501", 501, "worker", false, "L200", "p1-500")
	seedLaneSession(t, db, "p1-502", 502, "worker", true, "L200", "p1-500")
	// An un-laned worker spawned by the default orchestrator keeps its parent.
	seedLaneSession(t, db, "p1-600", 600, "worker", false, "", "p1-1")
	seedLaneReport(t, db, "r108", "p1-284", "L108")
	seedLaneReport(t, db, "r200", "p1-501", "L200")
}

const liveLaneRows = "p1-500:L200: p1-501:L200:p1-500 p1-502:L200:p1-500 p1-600::p1-1"

func assertB108Swept(t *testing.T, db *sql.DB) {
	t.Helper()
	if got := laneRows(t, db); got != liveLaneRows {
		t.Fatalf("session_lane = %q, want %q", got, liveLaneRows)
	}
	if got := queryStrings(t, db, `SELECT session_id FROM ended_lane_orchestrators`); got != "p1-283" {
		t.Fatalf("ended lane orchestrators = %q, want p1-283", got)
	}
	if got := queryStrings(t, db, `SELECT id || ':' || lane_item_id FROM reports`); got != "r108: r200:L200" {
		t.Fatalf("reports = %q", got)
	}
	// The ended sessions stay in history.
	if got := queryStrings(t, db, `SELECT id FROM sessions WHERE id IN ('p1-283', 'p1-284', 'p1-392')`); got != "p1-283 p1-284 p1-392" {
		t.Fatalf("history = %q", got)
	}
}

func TestLaneLifecycleMigrationSweepsEndedLanes(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeLaneLifecycle)
	seedB108(t, db)
	gooseTo(t, db, true, laneLifecycle)
	assertB108Swept(t, db)

	// Idempotent: a second sweep (down drops only the marker table) finds no
	// ended lane and touches nothing else.
	gooseTo(t, db, false, beforeLaneLifecycle)
	gooseTo(t, db, true, laneLifecycle)
	if got := laneRows(t, db); got != liveLaneRows {
		t.Fatalf("session_lane after re-sweep = %q", got)
	}
	if got := queryStrings(t, db, `SELECT id || ':' || lane_item_id FROM reports`); got != "r108: r200:L200" {
		t.Fatalf("reports after re-sweep = %q", got)
	}
}

func terminate(t *testing.T, db *sql.DB, id string, terminated bool) {
	t.Helper()
	if _, err := db.Exec(`UPDATE sessions SET is_terminated = ? WHERE id = ?`, terminated, id); err != nil {
		t.Fatalf("terminate %s: %v", id, err)
	}
}

func TestLaneEndsWhenItsOrchestratorTerminates(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeLaneLifecycle)
	seedB108(t, db)
	gooseTo(t, db, true, laneLifecycle)

	// Neither a worker nor the default orchestrator ends a lane.
	terminate(t, db, "p1-501", true)
	terminate(t, db, "p1-1", true)
	if got := laneRows(t, db); got != liveLaneRows {
		t.Fatalf("session_lane after worker/default stop = %q", got)
	}
	terminate(t, db, "p1-501", false)

	terminate(t, db, "p1-500", true)
	if got := laneRows(t, db); got != "p1-600::p1-1" {
		t.Fatalf("session_lane after lane end = %q", got)
	}
	if got := queryStrings(t, db, `SELECT session_id FROM ended_lane_orchestrators`); got != "p1-283 p1-500" {
		t.Fatalf("ended = %q", got)
	}
	if got := queryStrings(t, db, `SELECT id || ':' || lane_item_id FROM reports`); got != "r108: r200:" {
		t.Fatalf("reports = %q", got)
	}

}

// A second live orchestrator of the same lane keeps the lane open.
func TestLaneStaysWhileAnotherOrchestratorRuns(t *testing.T) {
	db := openMigratedDatabaseCopy(t, beforeLaneLifecycle)
	seedB108(t, db)
	gooseTo(t, db, true, laneLifecycle)
	seedLaneSession(t, db, "p1-503", 503, "orchestrator", false, "L200", "")

	terminate(t, db, "p1-500", true)
	if got := laneRows(t, db); got != "p1-500:L200: p1-501:L200:p1-500 p1-502:L200:p1-500 p1-503:L200: p1-600::p1-1" {
		t.Fatalf("session_lane = %q", got)
	}
	terminate(t, db, "p1-503", true)
	if got := laneRows(t, db); got != "p1-600::p1-1" {
		t.Fatalf("session_lane after last orchestrator = %q", got)
	}
	if got := queryStrings(t, db, `SELECT session_id FROM ended_lane_orchestrators`); got != "p1-283 p1-500 p1-503" {
		t.Fatalf("ended = %q", got)
	}
}
