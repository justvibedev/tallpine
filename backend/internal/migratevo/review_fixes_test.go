package migratevo

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
)

func (f *fixture) openDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(f.env.SourceDB(), ""))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// A live Claude session on an account runs with CLAUDE_CONFIG_DIR under the
// state root; its transcripts must be copied inside that (moved) config dir.
func TestClaudeCopyInAccountConfigDir(t *testing.T) {
	f := newFixture(t)
	e := f.env
	db := f.openDB(t)
	cwd := f.wts[1]
	cfg := filepath.Join(e.Source, "agent-accounts", "a1")
	f.exec(t, db, `INSERT INTO agent_native_sessions (id, ao_session_id, harness, config_dir, created_at, last_used_at)
	               VALUES ('n1','proj-2','claude-code',?, '2026-01-01','2026-01-01')`, cfg)
	oldName, _ := ClaudeProjectDirName(cwd)
	writeFile(t, filepath.Join(cfg, "projects", oldName, "s.jsonl"), "hist\n")

	plan, err := e.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	var got *ClaudeCopy
	for i, c := range plan.ClaudeCopies {
		if c.Session == "proj-2" && c.Skip == "" {
			got = &plan.ClaudeCopies[i]
		}
	}
	if got == nil || !strings.HasPrefix(got.Src, filepath.Join(e.Target, "agent-accounts", "a1")) {
		t.Fatalf("account copy not planned inside the moved config dir: %+v", plan.ClaudeCopies)
	}
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	if n, err := e.CopyClaudeDirs(context.Background(), plan.ClaudeCopies); err != nil || n < 1 {
		t.Fatalf("copied %d, %v", n, err)
	}
	newName, _ := ClaudeProjectDirName(strings.Replace(cwd, e.Source, e.Target, 1))
	if b, err := os.ReadFile(filepath.Join(e.Target, "agent-accounts", "a1", "projects", newName, "s.jsonl")); err != nil || string(b) != "hist\n" {
		t.Fatalf("history missing next to the moved account dir: %v", err)
	}
}

// Workspace sessions get one extra worktree per child repo; they must be
// repaired too.
func TestRepairChildRepoWorktrees(t *testing.T) {
	f := newFixture(t)
	e := f.env
	db := f.openDB(t)
	wsProj := filepath.Join(e.Home, "ws")
	child := filepath.Join(wsProj, "child")
	mustRun(t, "", "git", "init", "-q", child)
	mustRun(t, child, "git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "i")
	root := filepath.Join(e.Source, "data", "worktrees", "ws", "ws-1")
	childWT := filepath.Join(root, "child")
	mustRun(t, child, "git", "worktree", "add", "-q", "-b", "c1", childWT)
	f.exec(t, db, `INSERT INTO projects (id, path, kind, registered_at) VALUES ('ws', ?, 'workspace', '2026-01-01')`, wsProj)
	f.exec(t, db, `INSERT INTO workspace_repos (project_id, name, relative_path, registered_at) VALUES ('ws','child','child','2026-01-01')`)
	f.exec(t, db, `INSERT INTO sessions (id, project_id, num, harness, workspace_path, activity_last_at, created_at, updated_at)
	               VALUES ('ws-1','ws',1,'claude-code',?, '2026-01-01','2026-01-01','2026-01-01')`, root)
	f.exec(t, db, `INSERT INTO session_worktrees (session_id, repo_name, branch, base_sha, worktree_path) VALUES ('ws-1','child','c1','x',?)`, childWT)

	plan, err := e.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	if _, fl := e.RepairWorktrees(context.Background(), plan.Worktrees); len(fl) > 0 {
		t.Fatal(fl)
	}
	newChild := strings.Replace(childWT, e.Source, e.Target, 1)
	if out, err := e.Run(context.Background(), child, "git", "worktree", "list", "--porcelain"); err != nil || !strings.Contains(string(out), newChild) || strings.Contains(string(out), "prunable") {
		t.Fatalf("child worktree not repaired: %v\n%s", err, out)
	}
}

// The preconditions must keep recognising the OLD install after the brand flip.
func TestLegacyIdentityPinned(t *testing.T) {
	if brand.LegacyCLIName != "vo" || brand.LegacyProductName != "Vibe Orchestrator" || brand.LegacyTmuxSocketName != "vo" {
		t.Fatalf("legacy identity changed: %q %q %q", brand.LegacyCLIName, brand.LegacyProductName, brand.LegacyTmuxSocketName)
	}
	if got := firstOf(liveProcesses([]string{"7 vo daemon"}, "/h/.vo", 1)); got == "" {
		t.Error("legacy vo daemon not flagged")
	}
	if got := firstOf(liveProcesses([]string{"8 /Apps/Vibe Orchestrator.app/Contents/x"}, "/h/.vo", 1)); got == "" {
		t.Error("legacy app not flagged")
	}
}

func TestOrphanClassification(t *testing.T) {
	src := "/h/.vo"
	lines := []string{
		"10 /h/.vo/data/tmp/go-build123/b001/exe/foo.test -test.run X",
		"11 /h/.vo/data/tmp/session-1/x/bar.test",
		"12 /h/.vo/data/worktrees/p/wt/server",
		"13 /h/.vo/data/tmpfoo/evil",
		"14 /h/.vo/data/tmp/../daemon/vo daemon",
		"15 vo daemon",
		"16 /Apps/Vibe Orchestrator.app/Contents/MacOS/x",
		"17 /h/.vo/data/tmp/self.test",
		"1 /h/.vo/data/tmp/init",
	}
	got := orphanProcesses(lines, src, 17)
	if len(got) != 2 || got[0].PID != 10 || got[1].PID != 11 {
		t.Fatalf("orphans = %+v", got)
	}
	err := blockedByProcessesError(liveProcesses(lines, src, 17), got)
	for _, want := range []string{"10 /h/.vo/data/tmp/go-build123", "kill 10 11", "--stop-orphans", "15 vo daemon"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}
}

func TestStopOrphansOnlySignalsOrphansThenRechecks(t *testing.T) {
	f := newFixture(t)
	e := f.env
	orphan := "10 " + e.Source + "/data/tmp/go-build/foo.test"
	app := "11 /Apps/" + brand.LegacyProductName + ".app/Contents/MacOS/x"
	alive := []string{orphan, app}
	var killed []int
	e.Processes = func(context.Context) ([]string, error) { return alive, nil }
	e.AliveTmux = func(context.Context) bool { return false }
	e.Kill = func(pid int) error {
		killed = append(killed, pid)
		alive = []string{app}
		return nil
	}
	if err := e.CheckPreconditions(context.Background(), false); err == nil || !strings.Contains(err.Error(), "kill 10") {
		t.Fatalf("without --stop-orphans expected a kill hint, got %v", err)
	}
	if len(killed) != 0 {
		t.Fatalf("signalled without --stop-orphans: %v", killed)
	}
	e.StopOrphans = true
	err := e.CheckPreconditions(context.Background(), false)
	if len(killed) != 1 || killed[0] != 10 {
		t.Fatalf("killed %v, want only the orphan", killed)
	}
	if err == nil || !strings.Contains(err.Error(), "11 /Apps/") || strings.Contains(err.Error(), "kill 11") {
		t.Fatalf("app must still block and never be signalled: %v", err)
	}
}

func TestStaleEnvProcessesWarnedNotBlocking(t *testing.T) {
	f := newFixture(t)
	e := f.env
	dev := "20 /usr/bin/node " + e.Source + "/data/worktrees/p/p-1/node_modules/.bin/nuxt dev"
	e.Processes = func(context.Context) ([]string, error) { return []string{dev, "21 /usr/bin/other"}, nil }
	e.AliveTmux = func(context.Context) bool { return false }
	if err := e.CheckPreconditions(context.Background(), false); err != nil {
		t.Fatalf("worktree dev server must not block: %v", err)
	}
	if out := f.out.String(); !strings.Contains(out, "TMPDIR") || !strings.Contains(out, dev) || strings.Contains(out, "21 /usr/bin/other") {
		t.Fatalf("warning output: %q", out)
	}
}

func firstOf(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return l[0]
}
