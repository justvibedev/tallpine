package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// legacyMergeDigestPaths is the path query MergeDigestPRs used before the PR
// filter was written once: the whole filter repeated beside pr_paths.
const legacyMergeDigestPaths = `
SELECT pp.pr_url, pp.path
FROM pr_paths pp
JOIN pr p ON p.url = pp.pr_url
JOIN sessions s ON s.id = p.session_id
WHERE s.project_id = ?
  AND (p.pr_state IN ('open', 'draft')
       OR (p.pr_state = 'merged' AND COALESCE(p.merged_at_provider, p.state_changed_at, p.updated_at) >= ?))`

// The digest's paths come from the PRs its filter selected, so they must equal
// what the old repeated-filter query returned, for every PR state and project.
func TestMergeDigestPathsMatchLegacyQuery(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	since := now.Add(-24 * time.Hour)
	for _, p := range []string{"p1", "p2"} {
		if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: p, Path: "/tmp/" + p, RegisteredAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	type seed struct {
		project, state string
		mergedAt       time.Time
		paths          []string
	}
	seeds := []seed{
		{"p1", "open", time.Time{}, []string{"b.go", "a.go"}},
		{"p1", "draft", time.Time{}, []string{"d.go"}},
		{"p1", "merged", now.Add(-time.Hour), []string{"m.go", "n.go"}},
		{"p1", "merged", now.Add(-72 * time.Hour), []string{"old.go"}},
		{"p1", "closed", time.Time{}, []string{"closed.go"}},
		{"p1", "open", time.Time{}, nil},
		{"p2", "open", time.Time{}, []string{"other.go"}},
	}
	db, err := sql.Open("sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i, sd := range seeds {
		rec := domain.SessionRecord{
			ProjectID: domain.ProjectID(sd.project), Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
			Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now,
		}
		created, err := s.CreateSession(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		url := fmt.Sprintf("https://example/pr/%d", i)
		if err := s.WritePR(ctx, domain.PullRequest{URL: url, SessionID: created.ID, Number: i + 1, UpdatedAt: now, Paths: sd.paths}, nil, nil); err != nil {
			t.Fatal(err)
		}
		var merged any
		if !sd.mergedAt.IsZero() {
			merged = sd.mergedAt
		}
		if _, err := db.Exec(`UPDATE pr SET pr_state = ?, merged_at_provider = ? WHERE url = ?`, sd.state, merged, url); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.MergeDigestPRs(ctx, "p1", since)
	if err != nil {
		t.Fatal(err)
	}
	gotPaths := map[string][]string{}
	for _, pr := range got {
		if len(pr.Paths) > 0 {
			gotPaths[pr.URL] = pr.Paths
		}
	}

	rows, err := db.Query(legacyMergeDigestPaths, "p1", since)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string][]string{}
	for rows.Next() {
		var url, path string
		if err := rows.Scan(&url, &path); err != nil {
			t.Fatal(err)
		}
		want[url] = append(want[url], path)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, v := range want {
		sort.Strings(v)
	}
	if len(got) != 4 {
		t.Fatalf("digest PRs = %d, want 4 (open, draft, recent merge, pathless open)", len(got))
	}
	if !reflect.DeepEqual(gotPaths, want) {
		t.Fatalf("digest paths = %v, legacy query = %v", gotPaths, want)
	}
	if len(want) != 3 {
		t.Fatalf("legacy query matched %d PRs, want 3; fixture is wrong", len(want))
	}
}
