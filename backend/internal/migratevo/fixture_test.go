package migratevo

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

type fixture struct {
	env  *Env
	repo string   // main repo, outside the source root
	wts  []string // worktrees under the source root (old paths)
	out  *safeBuf
}

type safeBuf struct{ b []byte }

func (s *safeBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *safeBuf) String() string              { return string(s.b) }

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newFixture builds a fake home with a real-schema ao.db, one git repo with
// two worktrees under the source root, and Claude project dirs.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := NewRoots(home)
	f := &fixture{out: &safeBuf{}}
	f.repo = filepath.Join(home, "repos", "proj")
	mustRun(t, "", "git", "init", "-q", f.repo)
	mustRun(t, f.repo, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	for _, n := range []string{"wt1", "wt2"} {
		wt := filepath.Join(r.Source, "data", "worktrees", "proj", n)
		mustRun(t, f.repo, "git", "worktree", "add", "-q", "-b", n, wt)
		f.wts = append(f.wts, wt)
	}
	dataDir := filepath.Join(r.Source, "data")
	store, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db, err := sql.Open("sqlite", dsn(r.SourceDB(), ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f.exec(t, db, `INSERT INTO projects (id, path, registered_at) VALUES ('proj', ?, '2026-01-01')`, f.repo)
	ins := `INSERT INTO sessions (id, project_id, num, harness, is_terminated, workspace_path, artifact_dir, prompt, activity_last_at, created_at, updated_at)
	        VALUES (?, 'proj', ?, ?, ?, ?, ?, ?, '2026-01-01', '2026-01-01', '2026-01-01')`
	prompt := "see " + r.Source + "/data/old in history"
	f.exec(t, db, ins, "proj-1", 1, "claude-code", 0, f.wts[0], r.Source+"/data/artifacts/proj-1", prompt)
	f.exec(t, db, ins, "proj-2", 2, "claude-code", 0, f.wts[1], r.Source+"/data/artifacts/proj-2", prompt)
	f.exec(t, db, ins, "proj-3", 3, "claude-code", 1, filepath.Join(r.Source, "data", "worktrees", "proj", "gone"), "", "")
	f.exec(t, db, ins, "proj-4", 4, "codex", 0, filepath.Join(r.Source, "data", "worktrees", "proj", "plain"), "", "")
	f.exec(t, db, ins, "proj-5", 5, "claude-code", 0, "/elsewhere/not/moved", "", "")
	f.exec(t, db, `INSERT INTO reports (id, session_id, project_id, created_at, available_at) VALUES ('r1','proj-1','proj','2026-01-01','2026-01-01')`)
	f.exec(t, db, `INSERT INTO report_outputs (report_id, position, kind, reference) VALUES ('r1',0,'artifact',?), ('r1',1,'pr_reviewed','https://github.com/o/r/pull/1'), ('r1',2,'artifact',?)`,
		r.Source+"/data/artifacts/proj-1/plan.md", r.Source+"2/data/lookalike")
	for _, w := range append([]string{filepath.Join(r.Source, "data", "worktrees", "proj", "gone")}, f.wts[0]) {
		name, _ := ClaudeProjectDirName(w)
		writeFile(t, filepath.Join(r.ClaudeProjects, name, "sess.jsonl"), "{\"cwd\":\""+w+"\"}\n")
	}
	f.env = &Env{Roots: r, Run: ExecRunner, SelfPID: os.Getpid(), Out: f.out}
	return f
}

func (f *fixture) exec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) query1(t *testing.T, dbPath, q string, args ...any) string {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(dbPath, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var s string
	if err := db.QueryRow(q, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return s
}
