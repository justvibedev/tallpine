package cli

// import_official_e2e_test.go drives `tallpine import ao` through the REAL router, the REAL
// AO-import controller and service, and a REAL migrated SQLite registry, with an
// official-shaped ao.db as the read-only source. It also guards the CLI's
// hand-mirrored request/response structs against drift from the controller.

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/runfile"
	officialimportsvc "tallpine.eu/tallpine/backend/internal/service/officialimport"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// startImportDaemon serves the import route over a real registry in targetDir
// and points the CLI's run-file at it.
func startImportDaemon(t *testing.T, targetDir string) {
	t.Helper()
	store := sqlitetest.MustOpenAt(t, targetDir)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		AOImport: officialimportsvc.New(officialimportsvc.Deps{Store: store, Settings: &importSettings{}, TargetDataDir: targetDir}),
	}, httpd.ControlDeps{})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	rf := filepath.Join(t.TempDir(), "running.json")
	t.Setenv("TALLPINE_RUN_FILE", rf)
	if err := runfile.Write(rf, runfile.Info{PID: os.Getpid(), Port: srv.Listener.Addr().(*net.TCPAddr).Port, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
}

// importSettings is an in-memory preference service: tui, refresh on, nothing disabled.
type importSettings struct{ snap settingssvc.Snapshot }

func (m *importSettings) Get(context.Context) (settingssvc.Snapshot, error) {
	if m.snap.DefaultSessionMode == "" {
		m.snap = settingssvc.Snapshot{DefaultSessionMode: domain.SessionModeTUI, VersionRefresh: true}
	}
	return m.snap, nil
}

func (m *importSettings) SetDefaultSessionMode(ctx context.Context, mode domain.SessionMode) (settingssvc.Snapshot, error) {
	_, _ = m.Get(ctx)
	m.snap.DefaultSessionMode = mode
	return m.snap, nil
}

func (m *importSettings) SetVersionRefresh(ctx context.Context, on bool) (settingssvc.Snapshot, error) {
	_, _ = m.Get(ctx)
	m.snap.VersionRefresh = on
	return m.snap, nil
}

func (m *importSettings) SetHarnessDefault(ctx context.Context, h domain.AgentHarness, v domain.HarnessDefault) (settingssvc.Snapshot, error) {
	_, _ = m.Get(ctx)
	if m.snap.HarnessDefaults == nil {
		m.snap.HarnessDefaults = map[string]domain.HarnessDefault{}
	}
	m.snap.HarnessDefaults[string(h)] = v
	return m.snap, nil
}

func runImportCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := NewRootCommand(Deps{Out: &out, Err: &out, HTTPClient: &http.Client{}, ProcessAlive: func(int) bool { return true }})
	root.SetArgs(append([]string{"import", "ao"}, args...))
	err := root.Execute()
	return out.String(), err
}

func TestE2E_ImportOfficialDryRunThenSelectiveImport(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "official", "data")
	st, err := sqlitetest.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(source, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		dir := filepath.Join(root, "repos", id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at, config, kind)
			VALUES (?, ?, ?, '2026-10-24 21:29:10.606384 +0000 UTC', '{"worker":{"agent":"gemini"},"autoReview":true}', 'single_repo')`,
			id, dir, "Project "+id); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	target := filepath.Join(root, "tallpine", "data")
	startImportDaemon(t, target)

	// Dry run lists everything, writes nothing.
	out, err := runImportCLI(t, "--dry-run", "--from-data-dir", source)
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	for _, want := range []string{"Dry run -- nothing was written.", "Project one", "Project two", `worker harness "gemini"`, "2 create"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, out)
		}
	}

	// A real run needs an explicit scope.
	if _, err := runImportCLI(t, "--from-data-dir", source); err == nil {
		t.Fatal("real run without --project/--all must be refused")
	}

	// Gradual migration: one project first.
	out, err = runImportCLI(t, "--project", "one", "--from-data-dir", source)
	if err != nil {
		t.Fatalf("import one: %v\n%s", err, out)
	}
	if !strings.Contains(out, "create (done)") || !strings.Contains(out, "not selected") || !strings.Contains(out, "1 create") {
		t.Errorf("selective output:\n%s", out)
	}

	// Rerun, now all: one is unchanged, two is new.
	out, err = runImportCLI(t, "--all", "--from-data-dir", source)
	if err != nil {
		t.Fatalf("import all: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 create, 0 update, 1 unchanged") {
		t.Errorf("rerun must be idempotent:\n%s", out)
	}

	// Unknown id is a clean error.
	if out, err = runImportCLI(t, "--project", "ghost", "--from-data-dir", source); err == nil {
		t.Fatalf("unknown project must fail:\n%s", out)
	}
}

// TestImportOfficialAgainstRealSnapshot prints a dry run against a snapshot of a real
// install. It only runs when TALLPINE_IMPORT_REAL_SNAPSHOT names a data dir (for
// example a `cp -c` snapshot of ~/.ao/data); it writes the output to
// TALLPINE_IMPORT_REAL_OUT when set.
func TestImportOfficialAgainstRealSnapshot(t *testing.T) {
	snapshot := os.Getenv("TALLPINE_IMPORT_REAL_SNAPSHOT")
	if snapshot == "" {
		t.Skip("TALLPINE_IMPORT_REAL_SNAPSHOT not set")
	}
	startImportDaemon(t, filepath.Join(t.TempDir(), "tallpine", "data"))
	out, err := runImportCLI(t, "--dry-run", "--settings", "--from-data-dir", snapshot)
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if path := os.Getenv("TALLPINE_IMPORT_REAL_OUT"); path != "" {
		if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Log("\n" + out)
}
