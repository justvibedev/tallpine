package officialimport

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/officialimport"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// officialFixture builds a data dir shaped like an official install: this
// repo's migrated schema holding two projects, one a workspace with children.
func officialFixture(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "official", "data")
	st, err := sqlitetest.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, officialimport.DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	app, ws := filepath.Join(root, "app"), filepath.Join(root, "ws")
	for _, p := range []string{app, filepath.Join(ws, "api")} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO projects (id, path, repo_origin_url, display_name, registered_at, config, kind)
			VALUES ('app', ?, 'https://github.com/acme/app.git', 'App', '2026-10-24 21:29:10.606384 +0000 UTC',
			'{"worker":{"agent":"aider","agentConfig":{"model":"x"}},"reviewers":[{"harness":"codex","agentConfig":{"effort":"low"}}],"autoReview":true,"env":{"A":"b"}}', 'single_repo')`, []any{app}},
		{`INSERT INTO projects (id, path, repo_origin_url, display_name, registered_at, config, kind)
			VALUES ('ws', ?, '', 'WS', '2026-10-24 21:29:10.606384 +0000 UTC', NULL, 'workspace')`, []any{ws}},
		{`INSERT INTO workspace_repos (project_id, name, relative_path, repo_origin_url, registered_at, default_branch, git_status)
			VALUES ('ws', 'api', 'api', 'https://github.com/acme/api.git', '2026-10-24 21:30:00.000000 +0000 UTC', 'trunk', 'ready')`, nil},
		{`UPDATE app_settings SET default_session_mode = 'chat', harness_defaults = '{"claude-code":{"disabledModels":["claude-opus-4-6"]}}'`, nil},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestImportThroughRealStoreIsIdempotent exercises the actual SQLite registry
// and settings service, which is what makes "unchanged on rerun" trustworthy.
func TestImportThroughRealStoreIsIdempotent(t *testing.T) {
	root := t.TempDir()
	source := officialFixture(t, root)
	targetDir := filepath.Join(root, "tallpine", "data")
	store := sqlitetest.MustOpenAt(t, targetDir)
	settings := &memSettings{snap: settingssvc.Snapshot{DefaultSessionMode: domain.SessionModeTUI, VersionRefresh: true}}
	svc := New(Deps{Store: store, Settings: settings, TargetDataDir: targetDir})
	ctx := context.Background()
	in := RunInput{SourceDataDir: source, IncludeSettings: true}

	rep, err := svc.Run(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Create != 2 || rep.HasFailures() {
		t.Fatalf("first run: %+v %+v", rep.Counts, rep.Projects)
	}
	app, ok, err := store.GetProject(ctx, "app")
	if err != nil || !ok {
		t.Fatalf("app missing: %v %v", ok, err)
	}
	if app.Config.Worker.Harness != domain.HarnessClaudeCode || app.Config.Worker.AgentConfig.Model != "" || app.Config.AutoReview == nil || !*app.Config.AutoReview {
		t.Fatalf("stored config = %+v", app.Config)
	}
	repos, err := store.ListWorkspaceRepos(ctx, "ws")
	if err != nil || len(repos) != 1 || repos[0].DefaultBranch != "trunk" {
		t.Fatalf("repos = %+v err=%v", repos, err)
	}
	snap, err := settings.Get(ctx)
	if err != nil || snap.DefaultSessionMode != domain.SessionModeChat || len(snap.HarnessDefaults["claude-code"].DisabledModels) != 1 {
		t.Fatalf("settings = %+v err=%v", snap, err)
	}

	rep, err = svc.Run(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Unchanged != 2 || rep.Counts.Create+rep.Counts.Update != 0 {
		t.Fatalf("rerun must be a no-op: %+v", rep.Counts)
	}
	for _, s := range rep.Settings {
		if s.Action != officialimport.ActionUnchanged {
			t.Errorf("setting %s = %s on rerun", s.Key, s.Action)
		}
	}
}

func TestRefusesToReadItsOwnDataDir(t *testing.T) {
	dir := t.TempDir()
	svc := New(Deps{TargetDataDir: dir})
	if _, err := svc.Run(context.Background(), RunInput{SourceDataDir: dir}); err == nil {
		t.Fatal("source == target must be refused")
	}
}

// memSettings is an in-memory SettingsService.
type memSettings struct{ snap settingssvc.Snapshot }

func (m *memSettings) Get(context.Context) (settingssvc.Snapshot, error) { return m.snap, nil }

func (m *memSettings) SetDefaultSessionMode(_ context.Context, mode domain.SessionMode) (settingssvc.Snapshot, error) {
	m.snap.DefaultSessionMode = mode
	return m.snap, nil
}

func (m *memSettings) SetVersionRefresh(_ context.Context, on bool) (settingssvc.Snapshot, error) {
	m.snap.VersionRefresh = on
	return m.snap, nil
}

func (m *memSettings) SetHarnessDefault(_ context.Context, h domain.AgentHarness, v domain.HarnessDefault) (settingssvc.Snapshot, error) {
	if m.snap.HarnessDefaults == nil {
		m.snap.HarnessDefaults = map[string]domain.HarnessDefault{}
	}
	m.snap.HarnessDefaults[string(h)] = v
	return m.snap, nil
}

// TestRunRejectsCaseVariantOfOwnDataDir covers a source spelled differently
// from the target but naming the same directory. A string compare of the two
// resolved paths misses it; the filesystem identity check does not.
func TestRunRejectsCaseVariantOfOwnDataDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Tallpine", "data")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(root, "tallpine", "data")
	if _, err := os.Stat(variant); err != nil {
		t.Skip("filesystem is case-sensitive; no case-variant spelling exists")
	}
	svc := New(Deps{TargetDataDir: target})
	_, err := svc.Run(context.Background(), RunInput{SourceDataDir: variant})
	if err == nil || !strings.Contains(err.Error(), "own data dir") {
		t.Fatalf("expected source-is-target refusal, got %v", err)
	}
}
