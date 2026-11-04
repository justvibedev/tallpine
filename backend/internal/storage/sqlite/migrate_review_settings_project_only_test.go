package sqlite

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const oldDefaultRiskPaths = `["backend/internal/service/{chat,report,turnnotify,review}/**","backend/internal/storage/**","backend/internal/session_manager/**","backend/internal/httpd/**","**/migrations/**"]`

func reviewSettingsMigrationUp(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("migrations/20261103130000000_review_settings_project_only.sql")
	if err != nil {
		t.Fatal(err)
	}
	up := string(raw)
	up = up[strings.Index(up, "-- +goose Up")+len("-- +goose Up") : strings.Index(up, "-- +goose Down")]
	return up
}

func reviewSettingsProjectConfig(t *testing.T, db *sql.DB, id string) map[string]any {
	t.Helper()
	var cfg sql.NullString
	if err := db.QueryRow(`SELECT config FROM projects WHERE id = ?`, id).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if cfg.Valid {
		if err := json.Unmarshal([]byte(cfg.String), &out); err != nil {
			t.Fatalf("project %s config %q: %v", id, cfg.String, err)
		}
	}
	return out
}

func riskOf(cfg map[string]any) map[string]any {
	r, _ := cfg["reviewRisk"].(map[string]any)
	if r == nil {
		return map[string]any{}
	}
	return r
}

func openReviewSettingsDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedProject(t *testing.T, db *sql.DB, id string, config any) {
	t.Helper()
	mustExec(t, db, `INSERT INTO projects (id, path, registered_at, config) VALUES (?, ?, CURRENT_TIMESTAMP, ?)`, id, "/tmp/"+id, config)
}

func TestReviewSettingsMigrationAppOn(t *testing.T) {
	db := openReviewSettingsDB(t)
	mustExec(t, db, `UPDATE app_settings SET review_defaults = ? WHERE id = 1`,
		`{"autoReview":true,"terminateOnPrMerge":true,"reviewRisk":{"model":"claude-fable-5-1","paths":["docs/**"],"firstReviewOnly":true}}`)
	seedProject(t, db, "bare", nil)
	seedProject(t, db, "empty", `{}`)
	seedProject(t, db, "own", `{"autoReview":false,"terminateOnPrMerge":false,"reviewRisk":{"model":"claude-sonnet-5-5","paths":["a/**"],"harness":"codex"}}`)

	up := reviewSettingsMigrationUp(t)
	mustExec(t, db, up)

	for _, id := range []string{"bare", "empty"} {
		cfg := reviewSettingsProjectConfig(t, db, id)
		r := riskOf(cfg)
		if cfg["autoReview"] != true || cfg["terminateOnPrMerge"] != true || r["firstReviewOnly"] != true ||
			r["model"] != "claude-fable-5-1" || r["harness"] != "claude-code" || r["effort"] != nil {
			t.Fatalf("%s = %v, want the app values copied (claude-code, no effort)", id, cfg)
		}
		if paths, _ := r["paths"].([]any); len(paths) != 1 || paths[0] != "docs/**" {
			t.Fatalf("%s paths = %v, want the app list", id, r["paths"])
		}
	}
	own := reviewSettingsProjectConfig(t, db, "own")
	r := riskOf(own)
	if own["autoReview"] != false || own["terminateOnPrMerge"] != false || r["model"] != "claude-sonnet-5-5" || r["harness"] != "codex" ||
		r["firstReviewOnly"] != true {
		t.Fatalf("own = %v: project values must win, first-review-only is project OR app", own)
	}
	if paths, _ := r["paths"].([]any); len(paths) != 1 || paths[0] != "a/**" {
		t.Fatalf("own paths = %v", r["paths"])
	}

	before := map[string]string{}
	for _, id := range []string{"bare", "empty", "own"} {
		before[id] = dumpConfig(t, db, id)
	}
	mustExec(t, db, up)
	for id, want := range before {
		if got := dumpConfig(t, db, id); got != want {
			t.Fatalf("rerun changed %s:\n%s\n%s", id, want, got)
		}
	}
}

func dumpConfig(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var cfg sql.NullString
	if err := db.QueryRow(`SELECT config FROM projects WHERE id = ?`, id).Scan(&cfg); err != nil {
		t.Fatal(err)
	}
	return cfg.String
}

func TestReviewSettingsMigrationAppOff(t *testing.T) {
	db := openReviewSettingsDB(t)
	seedProject(t, db, "bare", nil)
	seedProject(t, db, "model", `{"reviewRisk":{"model":"claude-fable-5-1"}}`)
	seedProject(t, db, "paths", `{"reviewRisk":{"paths":["x/**"]}}`)

	mustExec(t, db, reviewSettingsMigrationUp(t))

	bare := reviewSettingsProjectConfig(t, db, "bare")
	if _, set := bare["autoReview"]; set {
		t.Fatalf("autoReview written with the app value off: %v", bare)
	}
	if _, set := bare["terminateOnPrMerge"]; set {
		t.Fatalf("terminateOnPrMerge written with the app value off: %v", bare)
	}
	r := riskOf(bare)
	raw, _ := json.Marshal(r["paths"])
	if string(raw) != oldDefaultRiskPaths || r["model"] != nil || r["harness"] != nil {
		t.Fatalf("bare reviewRisk = %v, want the old 5-glob list and no model/harness (defaults apply)", r)
	}
	m := riskOf(reviewSettingsProjectConfig(t, db, "model"))
	if m["model"] != "claude-fable-5-1" || m["harness"] != "claude-code" {
		t.Fatalf("model project = %v, want harness claude-code added", m)
	}
	if p, _ := riskOf(reviewSettingsProjectConfig(t, db, "paths"))["paths"].([]any); len(p) != 1 || p[0] != "x/**" {
		t.Fatalf("own paths overwritten: %v", p)
	}
}
