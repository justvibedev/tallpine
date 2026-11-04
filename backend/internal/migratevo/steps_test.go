package migratevo

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/brand"
)

func TestRenameHomeAndResume(t *testing.T) {
	f := newFixture(t)
	e := f.env
	marker := filepath.Join(e.Source, "data", "marker")
	writeFile(t, marker, "x")
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.Target, "data", "marker")); err != nil {
		t.Fatalf("tree not moved: %v", err)
	}
	if _, err := os.Lstat(e.Source); err == nil {
		t.Fatal("old root must be removed, nothing left in the home dir")
	}
	// A README stub left by an older migrator is cleaned up on resume.
	writeFile(t, filepath.Join(e.Source, readmeName), "moved")
	if err := e.RenameHome(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if _, err := os.Lstat(e.Source); err == nil {
		t.Fatal("README stub not removed")
	}
	// Idempotent when already finished.
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	// Both sides holding data is refused, not merged.
	writeFile(t, filepath.Join(e.Source, "stray"), "x")
	if err := e.RenameHome(); err == nil {
		t.Fatal("expected refusal when both roots hold data")
	}
}

func TestRepairWorktrees(t *testing.T) {
	f := newFixture(t)
	e := f.env
	plan, err := e.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	list := func() string {
		out, err := e.Run(context.Background(), f.repo, "git", "worktree", "list", "--porcelain")
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	if !strings.Contains(list(), "prunable") {
		t.Fatal("precondition: the repo should see its moved worktrees as prunable")
	}
	n, failures := e.RepairWorktrees(context.Background(), plan.Worktrees)
	if len(failures) > 0 || n != 2 {
		t.Fatalf("repaired %d, failures %v", n, failures)
	}
	for _, wt := range f.wts {
		p := strings.Replace(wt, e.Source, e.Target, 1)
		if _, err := e.Run(context.Background(), p, "git", "status"); err != nil {
			t.Errorf("git status in %s: %v", p, err)
		}
	}
	if strings.Contains(list(), "prunable") {
		t.Error("worktrees still prunable after repair")
	}
	if n2, fl := e.RepairWorktrees(context.Background(), plan.Worktrees); n2 != 2 || len(fl) > 0 {
		t.Errorf("rerun not idempotent: %d %v", n2, fl)
	}
}

func TestPlanClaudeCopies(t *testing.T) {
	f := newFixture(t)
	plan, err := f.env.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ClaudeCopy{}
	for _, c := range plan.ClaudeCopies {
		by[c.Session] = c
	}
	if len(by) != 2 { // terminated proj-3 and codex proj-4 are excluded; proj-5 is outside the root
		t.Fatalf("copies for %v", by)
	}
	if c := by["proj-1"]; c.Skip != "" || c.Bytes == 0 {
		t.Errorf("proj-1 should copy: %+v", c)
	}
	if by["proj-2"].Skip == "" {
		t.Error("proj-2 has no Claude dir and must be skipped")
	}
	if _, ok := by["proj-3"]; ok {
		t.Error("terminated session must not be planned")
	}
	if p, _ := f.env.ComputePlan(context.Background(), false); len(p.ClaudeCopies) != 0 {
		t.Error("--copy-harness-history=false must plan no copies")
	}
}

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var b bytes.Buffer
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			t.Errorf("walk %s: %v", p, err)
			return err
		}
		b.WriteString(p + "\n")
		if d.Type().IsRegular() {
			data, _ := os.ReadFile(p)
			b.Write(data)
		}
		return nil
	})
	return b.String()
}

func TestCopyClaudeDirsCopyOnly(t *testing.T) {
	f := newFixture(t)
	e := f.env
	plan, _ := e.ComputePlan(context.Background(), true)
	before := map[string]string{}
	for _, c := range plan.ClaudeCopies {
		if c.Skip == "" {
			before[c.Src] = snapshotTree(t, c.Src)
		}
	}
	n, err := e.CopyClaudeDirs(context.Background(), plan.ClaudeCopies)
	if err != nil || n != 1 {
		t.Fatalf("copied %d, err %v", n, err)
	}
	var src, dst string
	for _, c := range plan.ClaudeCopies {
		if c.Skip == "" {
			src, dst = c.Src, c.Dst
		}
	}
	a, _ := os.ReadFile(filepath.Join(src, "sess.jsonl"))
	b, err := os.ReadFile(filepath.Join(dst, "sess.jsonl"))
	if err != nil || !bytes.Equal(a, b) {
		t.Fatalf("copy missing or differs: %v", err)
	}
	// Originals untouched: everything that existed before is byte-identical.
	for _, c := range plan.ClaudeCopies {
		if c.Skip == "" && snapshotTree(t, c.Src) != before[c.Src] {
			t.Errorf("original %s changed", c.Src)
		}
	}
	// Second run: destination exists, nothing copied, destination not rewritten.
	writeFile(t, filepath.Join(dst, "sess.jsonl"), "edited")
	if n, err := e.CopyClaudeDirs(context.Background(), plan.ClaudeCopies); err != nil || n != 0 {
		t.Fatalf("rerun copied %d, %v", n, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "sess.jsonl")); string(got) != "edited" {
		t.Error("existing destination was overwritten")
	}
}

func TestPreconditions(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	e.Processes = func(context.Context) ([]string, error) {
		return []string{"1 /sbin/launchd", "41 " + e.Source + "/data/worktrees/p/node_modules/.bin/esbuild --service", "42 " + e.Source + "/data/bin/daemon"}, nil
	}
	if err := e.CheckPreconditions(ctx, false); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Errorf("live process not refused: %v", err)
	}
	e.Processes = func(context.Context) ([]string, error) { return []string{"7 vo daemon --port 3101"}, nil }
	if err := e.CheckPreconditions(ctx, false); err == nil {
		t.Error("vo daemon not refused")
	}
	e.Processes = func(context.Context) ([]string, error) { return nil, nil }
	e.AliveTmux = func(context.Context) bool { return true }
	if err := e.CheckPreconditions(ctx, false); err == nil || !strings.Contains(err.Error(), "tmux") {
		t.Errorf("tmux not refused: %v", err)
	}
	e.AliveTmux = func(context.Context) bool { return false }
	if err := e.CheckPreconditions(ctx, false); err != nil {
		t.Fatalf("clean state refused: %v", err)
	}
	os.MkdirAll(e.Target, 0o755)
	if err := e.CheckPreconditions(ctx, false); err != nil {
		t.Errorf("bare app-created target must be tolerated: %v", err)
	}
	os.MkdirAll(filepath.Dir(e.TargetDB()), 0o755)
	os.WriteFile(e.TargetDB(), []byte("x"), 0o600)
	if err := e.CheckPreconditions(ctx, false); err == nil {
		t.Error("existing target with a DB not refused")
	}
	if err := e.CheckPreconditions(ctx, true); err != nil {
		t.Errorf("resume must tolerate existing target: %v", err)
	}
}

func TestLiveProcessIgnoresSelfAndUnrelated(t *testing.T) {
	lines := []string{"100 /usr/bin/vo migrate-from-vo", "5 /bin/zsh", "6 /Applications/Vibe Orchestrator.app/Contents/MacOS/x"}
	if got := firstOf(liveProcesses(lines[:2], "/h/.vo", 100)); got != "" {
		t.Errorf("self/unrelated flagged: %q", got)
	}
	if got := firstOf(liveProcesses(lines, "/h/.vo", 100)); !strings.Contains(got, "Vibe Orchestrator.app") {
		t.Errorf("app not flagged: %q", got)
	}
}

func TestInstallCLILinks(t *testing.T) {
	for _, binary := range []string{"vo", "tallpine"} { // pre- and post-flip layouts
		t.Run(binary, func(t *testing.T) {
			f := newFixture(t)
			e := f.env
			e.CLIDir, e.CLIBinary = filepath.Join(e.Home, "bin"), binary
			writeFile(t, filepath.Join(e.CLIDir, binary), "binary")
			for i := 0; i < 2; i++ {
				if err := e.InstallCLILinks(); err != nil {
					t.Fatal(err)
				}
			}
			if dst, err := os.Readlink(filepath.Join(e.CLIDir, "tp")); err != nil || dst != binary {
				t.Errorf("tp -> %q, %v; want %s", dst, err, binary)
			}
			if fi, err := os.Lstat(filepath.Join(e.CLIDir, binary)); err != nil || fi.Mode()&os.ModeSymlink != 0 {
				t.Errorf("running binary %s must not become a link to itself", binary)
			}
			if binary == "vo" {
				if dst, _ := os.Readlink(filepath.Join(e.CLIDir, "tallpine")); dst != "vo" {
					t.Errorf("tallpine -> %q", dst)
				}
			} else if dst, _ := os.Readlink(filepath.Join(e.CLIDir, "vo")); dst != binary {
				t.Errorf("vo -> %q, want %s", dst, binary)
			}
		})
	}
}

func TestInstallCLILinksSkipsSignedBundle(t *testing.T) {
	f := newFixture(t)
	e := f.env
	e.CLIDir, e.CLIBinary = filepath.Join(e.Home, "Tallpine.app", "Contents", "Resources", "bin"), "tallpine"
	writeFile(t, filepath.Join(e.CLIDir, "tallpine"), "binary")
	if err := e.InstallCLILinks(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(e.CLIDir, "tp")); err == nil {
		t.Error("wrote a link inside the app bundle")
	}
}

func TestMigrateEndToEnd(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	// Dry run changes nothing: no plan file, no target, source tree identical.
	before := snapshotTree(t, e.Source)
	if err := e.Migrate(ctx, Options{DryRun: true, CopyHarnessHistory: true}); err != nil {
		t.Fatal(err)
	}
	if snapshotTree(t, e.Source) != before {
		t.Error("dry run modified the source tree")
	}
	for _, p := range []string{e.Target, e.PlanFile, e.legacyHomePlanFile()} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("dry run created %s", p)
		}
	}
	if !strings.Contains(f.out.String(), "claude: copy") {
		t.Errorf("dry run output lacks the claude copy:\n%s", f.out)
	}

	counts := func() string {
		return f.query1(t, e.TargetDB(), `SELECT (SELECT count(*) FROM sessions)||','||(SELECT count(*) FROM report_outputs)||','||(SELECT count(*) FROM change_log)`)
	}
	if err := e.Migrate(ctx, Options{CopyHarnessHistory: true}); err != nil {
		t.Fatalf("migrate: %v\n%s", err, f.out)
	}
	rows := counts()
	if rows != "5,3,"+strings.Split(rows, ",")[2] {
		t.Errorf("row counts changed: %s", rows)
	}
	plan, _ := e.LoadPlan()
	for _, s := range []string{StepRename, StepDB, StepWorktrees, StepClaude, StepCLI} {
		if !plan.Done(s) {
			t.Errorf("step %s not marked done", s)
		}
	}
	if plan.Done(StepLaunchd) || plan.Done(StepDaemon) {
		t.Error("unwired hook steps must stay unmarked")
	}
	wt := strings.Replace(f.wts[0], e.Source, e.Target, 1)
	if _, err := e.Run(ctx, wt, "git", "status"); err != nil {
		t.Errorf("git status after migrate: %v", err)
	}

	// Rerun is a no-op.
	treeBefore := snapshotTree(t, e.Target)
	f.out.b = nil
	if err := e.Migrate(ctx, Options{CopyHarnessHistory: true}); err != nil {
		t.Fatal(err)
	}
	if snapshotTree(t, e.Target) != treeBefore || counts() != rows {
		t.Error("rerun changed state")
	}
}

func TestMigrateResumesAfterFailure(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	failing := true
	e.Run = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
		if failing && name == "git" && len(args) > 1 && args[1] == "repair" {
			return nil, errors.New("boom")
		}
		return ExecRunner(ctx, dir, name, args...)
	}
	if err := e.Migrate(ctx, Options{CopyHarnessHistory: true}); err == nil {
		t.Fatal("expected worktree step failure")
	}
	plan, _ := e.LoadPlan()
	if !plan.Done(StepRename) || !plan.Done(StepDB) || plan.Done(StepWorktrees) {
		t.Fatalf("steps after failure: %v", plan.Steps)
	}
	failing = false
	if err := e.Migrate(ctx, Options{CopyHarnessHistory: true}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if plan, _ = LoadPlan(e.PlanFile); !plan.Done(StepWorktrees) || !plan.Done(StepClaude) {
		t.Errorf("resume incomplete: %v", plan.Steps)
	}
}

// TestComputePlanSeesUncheckpointedWAL: rows still in the -wal file must be
// planned (the DB is cloned with its WAL), and the source files stay as they were.
func TestComputePlanSeesUncheckpointedWAL(t *testing.T) {
	f := newFixture(t)
	e := f.env
	db, err := sql.Open("sqlite", dsn(e.SourceDB(), "&_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	f.exec(t, db, `INSERT INTO sessions (id, project_id, num, harness, workspace_path, activity_last_at, created_at, updated_at)
	               VALUES ('proj-9', 'proj', 9, 'claude-code', ?, '2026-01-01', '2026-01-01', '2026-01-01')`, filepath.Join(e.Source, "data", "worktrees", "proj", "wt9"))
	if fi, err := os.Stat(e.SourceDB() + "-wal"); err != nil || fi.Size() == 0 {
		t.Skip("no pending WAL in this environment")
	}
	walBefore, _ := os.ReadFile(e.SourceDB() + "-wal")
	plan, err := e.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, c := range plan.ClaudeCopies {
		seen = seen || c.Session == "proj-9"
	}
	if !seen {
		t.Error("session that exists only in the WAL was not planned")
	}
	if walAfter, _ := os.ReadFile(e.SourceDB() + "-wal"); !bytes.Equal(walBefore, walAfter) {
		t.Error("planning modified the source WAL")
	}
}

func TestPlanListsMovingAccounts(t *testing.T) {
	f := newFixture(t)
	e := f.env
	db, err := sql.Open("sqlite", dsn(e.SourceDB(), ""))
	if err != nil {
		t.Fatal(err)
	}
	f.exec(t, db, `INSERT INTO agent_accounts (id, provider, home, created_at) VALUES ('a1','claude',?,'2026-01-01'),('a2','codex','/elsewhere/home','2026-01-01')`,
		e.Source+"/data/agents/a1")
	_ = db.Close()
	plan, err := e.ComputePlan(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Accounts) != 1 || plan.Accounts[0].ID != "a1" {
		t.Fatalf("accounts = %+v", plan.Accounts)
	}
	e.Describe(plan)
	if !strings.Contains(f.out.String(), "sign in again") {
		t.Errorf("no sign-in warning:\n%s", f.out)
	}
	if _, err := RewriteDB(context.Background(), e.SourceDB(), e.Source, e.Target); err != nil {
		t.Fatal(err)
	}
	if h := f.query1(t, e.SourceDB(), `SELECT home FROM agent_accounts WHERE id='a1'`); h != e.Target+"/data/agents/a1" {
		t.Errorf("home = %s", h)
	}
}

func TestPreconditionsRefuseLiveNewInstall(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	for name, line := range map[string]string{
		"app bundle": "9 /Users/x/Applications/" + brand.ProductName + ".app/Contents/MacOS/" + brand.ProductName,
		"daemon":     "9 /usr/local/bin/" + brand.CLIName + " daemon --port 3101",
		"alias":      "9 " + brand.CLIAlias + " start",
	} {
		e.Processes = func(context.Context) ([]string, error) { return []string{line}, nil }
		if err := e.CheckPreconditions(ctx, false); err == nil || !strings.Contains(err.Error(), "quit the app") {
			t.Errorf("%s: live new install not refused: %v", name, err)
		}
	}
	// a caller that only names the bundle in its arguments is not a live install
	e.Processes = func(context.Context) ([]string, error) {
		return []string{"8 /bin/bash scripts/release-local install /x/" + brand.ProductName + ".app/"}, nil
	}
	if err := e.CheckPreconditions(ctx, false); err != nil {
		t.Errorf("release-local install argument mistaken for a live install: %v", err)
	}
	// the migrator itself, run out of the new bundle, is not a live install
	e.Processes = func(context.Context) ([]string, error) {
		return []string{"9 /x/" + brand.ProductName + ".app/Contents/Resources/daemon/" + brand.CLIName + " migrate-from-vo"}, nil
	}
	if err := e.CheckPreconditions(ctx, false); err != nil {
		t.Errorf("migrator mistaken for a live install: %v", err)
	}
	// a live pid in the target's running.json
	e.Processes = func(context.Context) ([]string, error) { return nil, nil }
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	writeFile(t, filepath.Join(e.Target, "running.json"), fmt.Sprintf(`{"pid":%d}`, cmd.Process.Pid))
	if err := e.CheckPreconditions(ctx, false); err == nil || !strings.Contains(err.Error(), "running.json") {
		t.Errorf("live running.json pid not refused: %v", err)
	}
}

// Worktrees under the source root that no session row owns (release-builds,
// dev/**) are found through the repo's worktree list and repaired too.
func TestRepairWorktreesNotInSessions(t *testing.T) {
	f := newFixture(t)
	e := f.env
	extra := []string{
		filepath.Join(e.Source, "release-builds", "abc123"),
		filepath.Join(e.Source, "dev", "feat", "x"),
	}
	for _, wt := range extra {
		mustRun(t, f.repo, "git", "worktree", "add", "-q", "--detach", wt)
	}
	outside := filepath.Join(filepath.Dir(f.repo), "outside-wt")
	mustRun(t, f.repo, "git", "worktree", "add", "-q", "--detach", outside)
	plan, err := e.ComputePlan(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, w := range plan.Worktrees {
		got[w.Old] = true
		if w.Old == outside {
			t.Errorf("worktree outside the source root planned: %s", w.Old)
		}
	}
	for _, wt := range extra {
		if !got[wt] {
			t.Errorf("worktree %s not planned", wt)
		}
	}
	if err := e.RenameHome(); err != nil {
		t.Fatal(err)
	}
	n, failures := e.RepairWorktrees(context.Background(), plan.Worktrees)
	if len(failures) > 0 || n != 4 {
		t.Fatalf("repaired %d, failures %v", n, failures)
	}
	out, err := e.Run(context.Background(), f.repo, "git", "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "prunable") {
		t.Errorf("worktrees still prunable:\n%s", out)
	}
}

func TestMigrateLeavesNothingInHome(t *testing.T) {
	f := newFixture(t)
	e := f.env
	if err := e.Migrate(context.Background(), Options{}); err != nil {
		t.Fatalf("migrate: %v\n%s", err, f.out)
	}
	if _, err := os.Stat(e.PlanFile); err != nil {
		t.Errorf("plan not under the new root: %v", err)
	}
	for _, p := range []string{e.Source, e.legacyHomePlanFile()} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("%s left behind", p)
		}
	}
}

func TestLoadPlanReadsLegacyHomeFile(t *testing.T) {
	r := NewRoots(t.TempDir())
	p := &Plan{Version: planVersion, Steps: map[string]bool{StepRename: true}}
	if err := p.Save(r.legacyHomePlanFile()); err != nil {
		t.Fatal(err)
	}
	got, err := r.LoadPlan()
	if err != nil || got == nil || !got.Done(StepRename) {
		t.Fatalf("legacy plan not read: %v %v", got, err)
	}
	if err := r.SavePlan(got); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(r.legacyHomePlanFile()); err == nil {
		t.Error("legacy plan not removed after re-save")
	}
	if _, err := os.Lstat(r.PlanFile); err != nil {
		t.Errorf("plan not moved to the new location: %v", err)
	}
}

func TestResumeWithSourceGoneStillChecksLiveness(t *testing.T) {
	f := newFixture(t)
	e := f.env
	ctx := context.Background()
	if err := e.RenameHome(); err != nil { // old root is gone, target in place
		t.Fatal(err)
	}
	e.Processes = func(context.Context) ([]string, error) { return nil, nil }
	e.AliveTmux = func(context.Context) bool { return false }
	if err := e.CheckPreconditions(ctx, true); err != nil {
		t.Fatalf("clean resume refused: %v", err)
	}
	e.Processes = func(context.Context) ([]string, error) {
		return []string{"7 /x/" + brand.ProductName + ".app/Contents/Resources/daemon/" + brand.CLIName + " daemon"}, nil
	}
	if err := e.CheckPreconditions(ctx, true); err == nil {
		t.Error("live new process not refused on resume")
	}
	e.Processes = func(context.Context) ([]string, error) { return nil, nil }
	e.AliveTmux = func(context.Context) bool { return true }
	if err := e.CheckPreconditions(ctx, true); err == nil {
		t.Error("live tmux not refused on resume")
	}
	e.AliveTmux = func(context.Context) bool { return false }
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	writeFile(t, filepath.Join(e.Target, "running.json"), fmt.Sprintf(`{"pid":%d}`, cmd.Process.Pid))
	if err := e.CheckPreconditions(ctx, true); err == nil || !strings.Contains(err.Error(), "running.json") {
		t.Errorf("live running.json not refused on resume: %v", err)
	}
}
