package sqlite

import (
	"database/sql"
	"testing"
)

func TestMigrateCanonicalRepositoryPreservesExplicitTrust(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 125)
	for _, seed := range []struct {
		id     string
		config any
	}{
		{"legacy", `{"defaultBranch":"main","worker":{"agent":"codex"}}`},
		{"explicit", `{"canonicalRepoURL":"https://gitlab.com/group/subgroup/repo"}`},
		{"empty", nil},
	} {
		if _, err := db.Exec(`INSERT INTO projects(id,path,repo_origin_url,registered_at,config) VALUES(?,?,?,CURRENT_TIMESTAMP,?)`, seed.id, "/repo/"+seed.id, "https://gitlab.com/alice/repo", seed.config); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var canonical, branch, agent string
	if err := db.QueryRow(`SELECT json_extract(config,'$.canonicalRepoURL'),json_extract(config,'$.defaultBranch'),json_extract(config,'$.worker.agent') FROM projects WHERE id='legacy'`).Scan(&canonical, &branch, &agent); err != nil {
		t.Fatal(err)
	}
	if canonical != "" || branch != "main" || agent != "codex" {
		t.Fatalf("legacy config changed: %q %q %q", canonical, branch, agent)
	}
	if err := db.QueryRow(`SELECT json_extract(config,'$.canonicalRepoURL') FROM projects WHERE id='explicit'`).Scan(&canonical); err != nil {
		t.Fatal(err)
	}
	if canonical != "https://gitlab.com/group/subgroup/repo" {
		t.Fatalf("explicit trust changed: %q", canonical)
	}
	// A later migration may give a project with no config its review settings;
	// what matters here is that no canonical repository trust was invented.
	var invented sql.NullString
	if err := db.QueryRow(`SELECT json_extract(config,'$.canonicalRepoURL') FROM projects WHERE id='empty'`).Scan(&invented); err != nil {
		t.Fatal(err)
	}
	if invented.Valid {
		t.Fatalf("canonical repository trust invented for an empty config: %q", invented.String)
	}
}
