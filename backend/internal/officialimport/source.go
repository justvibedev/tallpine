package officialimport

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // database/sql driver "sqlite"
)

// sourceProject is one row of the official projects table plus its children.
type sourceProject struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	Archived      bool
	Kind          string
	ConfigJSON    string
	Repos         []sourceRepo
}

type sourceRepo struct {
	Name          string
	RelativePath  string
	RepoOriginURL string
	DefaultBranch string
	GitStatus     string
	RegisteredAt  time.Time
}

// sourceSettings are the official app_settings columns that map across.
type sourceSettings struct {
	Present            bool
	DefaultSessionMode string
	VersionRefresh     *bool
	HarnessDefaults    string
	CloudOffering      bool
}

type sourceData struct {
	Projects []sourceProject
	Settings sourceSettings
	// Counts of rows left behind, for the "skipped" section.
	SessionCount      int
	ConversationCount int
	ReportCount       int
}

// openSnapshot opens the cloned database strictly read-only. The clone is ours,
// so the WAL index the open creates next to it never touches the official dir.
func openSnapshot(ctx context.Context, path string) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{
		"mode":    {"ro"},
		"_pragma": {"busy_timeout(5000)", "query_only(1)"},
	}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open snapshot: %w", err)
	}
	return db, nil
}

func readSource(ctx context.Context, db *sql.DB) (sourceData, error) {
	var out sourceData
	projects, err := readProjects(ctx, db)
	if err != nil {
		return out, err
	}
	repos, err := readRepos(ctx, db)
	if err != nil {
		return out, err
	}
	for i := range projects {
		projects[i].Repos = repos[projects[i].ID]
	}
	out.Projects = projects
	if out.Settings, err = readSettings(ctx, db); err != nil {
		return out, err
	}
	out.SessionCount = countRows(ctx, db, "sessions")
	out.ConversationCount = countRows(ctx, db, "conversations")
	out.ReportCount = countRows(ctx, db, "reports")
	return out, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	// table is always a literal from this package, never input.
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info('"+table+"')") //nolint:gosec // G202: fixed table names only
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols[name] = true
	}
	return cols, rows.Err()
}

// col selects a column when the official schema has it, else a typed default,
// so a source one migration older or newer than expected still reads.
func col(cols map[string]bool, name, fallback string) string {
	if cols[name] {
		return name
	}
	return fallback
}

func readProjects(ctx context.Context, db *sql.DB) ([]sourceProject, error) {
	cols, err := tableColumns(ctx, db, "projects")
	if err != nil {
		return nil, err
	}
	if !cols["id"] || !cols["path"] {
		return nil, fmt.Errorf("source database has no usable projects table")
	}
	//nolint:gosec // G202: col() only ever emits column names from the fixed whitelist below
	q := "SELECT id, path, " + col(cols, "repo_origin_url", "''") + ", " + col(cols, "display_name", "''") + ", " +
		col(cols, "registered_at", "NULL") + ", " + col(cols, "archived_at", "NULL") + ", " +
		col(cols, "kind", "'single_repo'") + ", " + col(cols, "config", "NULL") + " FROM projects ORDER BY id"
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []sourceProject
	for rows.Next() {
		var p sourceProject
		var registered, archived, config any
		if err := rows.Scan(&p.ID, &p.Path, &p.RepoOriginURL, &p.DisplayName, &registered, &archived, &p.Kind, &config); err != nil {
			return nil, fmt.Errorf("read projects: %w", err)
		}
		p.RegisteredAt = parseTime(registered)
		p.Archived = archived != nil && fmt.Sprint(archived) != ""
		p.ConfigJSON = asString(config)
		if p.Kind == "" {
			p.Kind = "single_repo"
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readRepos(ctx context.Context, db *sql.DB) (map[string][]sourceRepo, error) {
	cols, err := tableColumns(ctx, db, "workspace_repos")
	if err != nil || !cols["project_id"] {
		return nil, err
	}
	//nolint:gosec // G202: col() only ever emits column names from the fixed whitelist below
	q := "SELECT project_id, name, relative_path, " + col(cols, "repo_origin_url", "''") + ", " +
		col(cols, "default_branch", "''") + ", " + col(cols, "git_status", "'ready'") + ", " +
		col(cols, "registered_at", "NULL") + " FROM workspace_repos ORDER BY project_id, name"
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("read workspace repos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string][]sourceRepo{}
	for rows.Next() {
		var projectID string
		var r sourceRepo
		var registered any
		if err := rows.Scan(&projectID, &r.Name, &r.RelativePath, &r.RepoOriginURL, &r.DefaultBranch, &r.GitStatus, &registered); err != nil {
			return nil, fmt.Errorf("read workspace repos: %w", err)
		}
		r.RegisteredAt = parseTime(registered)
		out[projectID] = append(out[projectID], r)
	}
	return out, rows.Err()
}

func readSettings(ctx context.Context, db *sql.DB) (sourceSettings, error) {
	cols, err := tableColumns(ctx, db, "app_settings")
	if err != nil || len(cols) == 0 {
		return sourceSettings{}, err
	}
	q := "SELECT " + col(cols, "default_session_mode", "''") + ", " + col(cols, "version_refresh", "NULL") + ", " +
		col(cols, "harness_defaults", "'{}'") + ", " + col(cols, "cloud_offering", "0") + " FROM app_settings LIMIT 1"
	var s sourceSettings
	var refresh, cloud any
	err = db.QueryRowContext(ctx, q).Scan(&s.DefaultSessionMode, &refresh, &s.HarnessDefaults, &cloud)
	if errors.Is(err, sql.ErrNoRows) {
		return sourceSettings{}, nil
	}
	if err != nil {
		return sourceSettings{}, fmt.Errorf("read app settings: %w", err)
	}
	s.Present = true
	if refresh != nil {
		b := asInt(refresh) != 0
		s.VersionRefresh = &b
	}
	s.CloudOffering = asInt(cloud) != 0
	return s, nil
}

// countRows is best effort: a missing table or an error is just "unknown" (0).
func countRows(ctx context.Context, db *sql.DB, table string) int {
	var n int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		return 0
	}
	return n
}

func asString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func asInt(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	case bool:
		return b2i(t)
	case string:
		return b2i(t == "1" || strings.EqualFold(t, "true"))
	case []byte:
		return b2i(string(t) == "1")
	default:
		return 0
	}
}

// timeLayouts are the encodings the official store has written: RFC 3339 and
// Go's time.Time.String() form.
var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func parseTime(v any) time.Time {
	switch t := v.(type) {
	case time.Time:
		return t.UTC()
	case nil:
		return time.Time{}
	}
	s := strings.TrimSpace(asString(v))
	// Strip Go's monotonic-clock suffix ("m=+0.123") if a String() form leaked it.
	if i := strings.Index(s, " m="); i >= 0 {
		s = s[:i]
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
