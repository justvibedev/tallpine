package officialimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// fixture is a stand-in for an official install: a data dir whose ao.db was
// created by this repo's migrations (the same lineage the official schema
// comes from) and then filled with registry rows.
type fixture struct {
	t        *testing.T
	home     string // stands in for $HOME; holds project dirs
	dataDir  string // the "official" ~/.ao/data
	staging  string
	projects map[string]string // id -> dir
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, home: t.TempDir(), projects: map[string]string{}}
	f.dataDir = filepath.Join(f.home, "official", "data")
	f.staging = filepath.Join(f.home, "legacy", "data", "import-staging")
	store, err := sqlitetest.Open(f.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return f
}

// dir creates a real project directory so the existence check passes.
func (f *fixture) dir(id string) string {
	f.t.Helper()
	p := filepath.Join(f.home, "repos", id)
	if err := os.MkdirAll(filepath.Join(p, ".git"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.projects[id] = p
	return p
}

func (f *fixture) exec(q string, args ...any) {
	f.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(f.dataDir, DatabaseFile))
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(q, args...); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) project(id, name, kind, config string, archived bool) string {
	f.t.Helper()
	path := f.dir(id)
	var cfg any
	if config != "" {
		cfg = config
	}
	var arch any
	if archived {
		arch = "2026-10-01 10:00:00 +0000 UTC"
	}
	f.exec(`INSERT INTO projects (id, path, repo_origin_url, display_name, registered_at, archived_at, config, kind)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id, path, "https://github.com/acme/"+id+".git", name,
		"2026-10-24 21:29:10.606384 +0000 UTC", arch, cfg, kind)
	return path
}

func (f *fixture) child(projectID, name, rel, branch string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Join(f.projects[projectID], rel), 0o755); err != nil {
		f.t.Fatal(err)
	}
	f.exec(`INSERT INTO workspace_repos (project_id, name, relative_path, repo_origin_url, registered_at, default_branch, git_status)
		VALUES (?, ?, ?, ?, ?, ?, 'ready')`, projectID, name, rel, "https://github.com/acme/"+name+".git",
		"2026-10-24 21:30:00.000000 +0000 UTC", branch)
}

func (f *fixture) opts() Options {
	return Options{SourceDataDir: f.dataDir, StagingDir: f.staging}
}

const richConfig = `{
  "agentConfig": {"permissions": "bypass-permissions"},
  "worker": {"agent": "gemini", "agentConfig": {"model": "gemini-3", "effort": "high", "permissions": "auto"}},
  "orchestrator": {"agent": "claude-code", "agentConfig": {"model": "claude-opus-5-5", "effort": "high"}},
  "reviewers": [{"harness": "cursor", "agentConfig": {"effort": "medium"}}, {"harness": "codex", "agentConfig": {"effort": "low"}}],
  "trackerIntake": {},
  "containerReap": {"disabled": true},
  "autoReview": true,
  "canonicalRepoURL": "https://github.com/acme/rich.git",
  "defaultBranch": "develop",
  "symlinks": ["node_modules"],
  "postCreate": ["npm ci"],
  "verifyCommand": "npm test",
  "agentRules": "be nice",
  "env": {"FOO": "bar", "OPENAI_API_KEY": "sk-secret-value"},
  "futureThing": {"a": 1}
}`

func seed(f *fixture) {
	f.project("rich", "Rich Project", "single_repo", richConfig, false)
	f.project("plain", "plain", "single_repo", "", false)
	f.project("ws", "Workspace", "workspace", `{"defaultBranch":"main"}`, false)
	f.child("ws", "api", "api", "trunk")
	f.child("ws", "web", "web", "main")
	f.project("old", "Old", "single_repo", "", true)
	f.project("scratchy", "Scratch", "scratch", "", false)
	f.project("gone", "Gone", "single_repo", "", false)
	_ = os.RemoveAll(f.projects["gone"])
}

type fakeTarget struct {
	projects map[string]domain.ProjectRecord
	repos    map[string][]domain.WorkspaceRepoRecord
	writes   int
}

func newTarget() *fakeTarget {
	return &fakeTarget{projects: map[string]domain.ProjectRecord{}, repos: map[string][]domain.WorkspaceRepoRecord{}}
}

func (t *fakeTarget) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := t.projects[id]
	return p, ok, nil
}

func (t *fakeTarget) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	var out []domain.ProjectRecord
	for _, p := range t.projects {
		if p.ArchivedAt.IsZero() {
			out = append(out, p)
		}
	}
	return out, nil
}

func (t *fakeTarget) ListWorkspaceRepos(_ context.Context, id string) ([]domain.WorkspaceRepoRecord, error) {
	return t.repos[id], nil
}

func (t *fakeTarget) ImportWorkspaceProject(_ context.Context, r domain.ProjectRecord, repos []domain.WorkspaceRepoRecord) error {
	t.writes++
	t.projects[r.ID] = r
	t.repos[r.ID] = repos
	return nil
}

func byID(rep Report) map[string]ProjectPlan {
	m := map[string]ProjectPlan{}
	for _, p := range rep.Projects {
		m[p.ID] = p
	}
	return m
}

func TestDryRunPlansWithoutWriting(t *testing.T) {
	f := newFixture(t)
	seed(f)
	target := newTarget()
	opts := f.opts()
	opts.DryRun = true

	rep, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatal(err)
	}
	if target.writes != 0 || len(target.projects) != 0 {
		t.Fatalf("dry run wrote to target: %d writes", target.writes)
	}
	if !rep.Found || !rep.DryRun {
		t.Fatalf("report = %+v", rep)
	}
	plans := byID(rep)
	want := map[string]Action{
		"rich": ActionCreate, "plain": ActionCreate, "ws": ActionCreate,
		"old": ActionSkip, "scratchy": ActionSkip, "gone": ActionSkip,
	}
	for id, action := range want {
		if plans[id].Action != action {
			t.Errorf("%s action = %q (%s), want %q", id, plans[id].Action, plans[id].Reason, action)
		}
	}
	if plans["old"].Selected {
		t.Error("archived project must not be selectable")
	}
	if rep.Counts.Create != 3 || rep.Counts.Skip != 3 {
		t.Errorf("counts = %+v", rep.Counts)
	}
	if plans["ws"].ChildRepos != 2 {
		t.Errorf("ws children = %d", plans["ws"].ChildRepos)
	}
	if len(rep.Skipped) == 0 {
		t.Error("report must list what is skipped")
	}
}

func TestReportNeverEchoesConfigValues(t *testing.T) {
	f := newFixture(t)
	seed(f)
	opts := f.opts()
	opts.DryRun = true
	rep, err := Run(context.Background(), newTarget(), opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rep)
	blob := string(raw)
	for _, secret := range []string{"sk-secret-value", "be nice", "npm ci", "npm test"} {
		if strings.Contains(blob, secret) {
			t.Errorf("report leaks config value %q", secret)
		}
	}
	rich := byID(rep)["rich"]
	if !containsSub(rich.Warnings, "OPENAI_API_KEY") {
		t.Errorf("expected a credential-looking env warning, got %v", rich.Warnings)
	}
}

func TestApplyMapsConfigAndNormalizesRemovedHarnesses(t *testing.T) {
	f := newFixture(t)
	seed(f)
	target := newTarget()

	rep, err := Run(context.Background(), target, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Create != 3 || rep.HasFailures() {
		t.Fatalf("counts = %+v, report = %+v", rep.Counts, rep.Projects)
	}
	got := target.projects["rich"]
	if got.DisplayName != "Rich Project" || got.Path != f.projects["rich"] || got.Kind != domain.ProjectKindSingleRepo {
		t.Fatalf("record = %+v", got)
	}
	if want := time.Date(2026, 10, 24, 21, 29, 10, 606384000, time.UTC); !got.RegisteredAt.Equal(want) {
		t.Errorf("registeredAt = %v, want %v", got.RegisteredAt, want)
	}
	c := got.Config
	if c.Worker.Harness != domain.HarnessClaudeCode {
		t.Errorf("worker harness = %q, want claude-code", c.Worker.Harness)
	}
	if c.Worker.AgentConfig.Model != "" || c.Worker.AgentConfig.Effort != "" {
		t.Errorf("tuning for the removed harness must be cleared: %+v", c.Worker.AgentConfig)
	}
	if c.Worker.AgentConfig.Permissions != domain.PermissionModeAuto {
		t.Errorf("permissions are harness-neutral and must survive: %+v", c.Worker.AgentConfig)
	}
	if c.Orchestrator.AgentConfig.Model != "claude-opus-5-5" {
		t.Errorf("orchestrator = %+v", c.Orchestrator)
	}
	if len(c.Reviewers) != 2 || c.Reviewers[0].Harness != domain.ReviewerClaudeCode || c.Reviewers[1].Harness != domain.ReviewerCodex {
		t.Errorf("reviewers = %+v", c.Reviewers)
	}
	if c.Reviewers[1].AgentConfig.Effort != "low" {
		t.Errorf("kept reviewer lost its effort: %+v", c.Reviewers[1])
	}
	if c.AutoReview == nil || !*c.AutoReview || !c.ContainerReap.Disabled || c.DefaultBranch != "develop" || c.VerifyCommand != "npm test" ||
		c.AgentRules != "be nice" || c.CanonicalRepoURL != "https://github.com/acme/rich.git" ||
		len(c.Symlinks) != 1 || len(c.PostCreate) != 1 || c.Env["FOO"] != "bar" || c.Env["OPENAI_API_KEY"] != "sk-secret-value" {
		t.Errorf("config not carried over: %+v", c)
	}
	plan := byID(rep)["rich"]
	if !containsSub(plan.Warnings, `worker harness "gemini"`) || !containsSub(plan.Warnings, `reviewer 1 harness "cursor"`) {
		t.Errorf("harness normalization must warn: %v", plan.Warnings)
	}
	if !containsSub(plan.Warnings, `"futureThing"`) {
		t.Errorf("unsupported key must warn: %v", plan.Warnings)
	}

	ws := target.projects["ws"]
	if ws.Kind != domain.ProjectKindWorkspace || len(target.repos["ws"]) != 2 {
		t.Fatalf("workspace = %+v repos=%+v", ws, target.repos["ws"])
	}
	for _, r := range target.repos["ws"] {
		if r.Name == "api" && r.DefaultBranch != "trunk" {
			t.Errorf("child default branch = %q", r.DefaultBranch)
		}
	}
	if _, ok := target.projects["old"]; ok {
		t.Error("archived project imported")
	}
	if _, ok := target.projects["scratchy"]; ok {
		t.Error("scratch project imported")
	}
}

func TestRerunIsIdempotentAndUpdatesChanges(t *testing.T) {
	f := newFixture(t)
	seed(f)
	target := newTarget()
	ctx := context.Background()

	if _, err := Run(ctx, target, f.opts()); err != nil {
		t.Fatal(err)
	}
	writes := target.writes
	rep, err := Run(ctx, target, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if target.writes != writes || rep.Counts.Unchanged != 3 || rep.Counts.Create != 0 || rep.Counts.Update != 0 {
		t.Fatalf("rerun not idempotent: writes %d->%d counts=%+v", writes, target.writes, rep.Counts)
	}

	f.exec(`UPDATE projects SET config = '{"defaultBranch":"release"}' WHERE id = 'ws'`)
	f.exec(`UPDATE workspace_repos SET default_branch = 'dev' WHERE project_id = 'ws' AND name = 'web'`)
	rep, err = Run(ctx, target, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Update != 1 || rep.Counts.Unchanged != 2 {
		t.Fatalf("counts after source change = %+v", rep.Counts)
	}
	if target.projects["ws"].Config.DefaultBranch != "release" {
		t.Errorf("update not applied: %+v", target.projects["ws"].Config)
	}
}

func TestProjectSelection(t *testing.T) {
	f := newFixture(t)
	seed(f)
	target := newTarget()
	opts := f.opts()
	opts.ProjectIDs = []string{"plain"}

	rep, err := Run(context.Background(), target, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(target.projects) != 1 || target.projects["plain"].ID != "plain" {
		t.Fatalf("only the selected project may be written: %v", target.projects)
	}
	plans := byID(rep)
	if plans["rich"].Selected || plans["rich"].Applied || !plans["plain"].Applied {
		t.Errorf("selection flags wrong: rich=%+v plain=%+v", plans["rich"], plans["plain"])
	}
	if len(rep.Projects) != 6 {
		t.Errorf("report should still list every source project, got %d", len(rep.Projects))
	}

	opts.ProjectIDs = []string{"plain", "nope", "nada"}
	_, err = Run(context.Background(), target, opts)
	var unknown *UnknownProjectError
	if !errors.As(err, &unknown) || strings.Join(unknown.IDs, ",") != "nada,nope" {
		t.Fatalf("err = %v", err)
	}
}

func TestConflictsAreReportedNotOverwritten(t *testing.T) {
	f := newFixture(t)
	seed(f)
	target := newTarget()
	other := filepath.Join(f.home, "elsewhere")
	target.projects["plain"] = domain.ProjectRecord{ID: "plain", Path: other, Kind: domain.ProjectKindSingleRepo}
	target.projects["squatter"] = domain.ProjectRecord{ID: "squatter", Path: f.projects["rich"], Kind: domain.ProjectKindSingleRepo}

	rep, err := Run(context.Background(), target, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	plans := byID(rep)
	if plans["plain"].Action != ActionConflict || plans["plain"].Reason != domain.ProjectImportConflictSameIDDifferentActivePath {
		t.Errorf("plain = %+v", plans["plain"])
	}
	if plans["rich"].Action != ActionConflict || plans["rich"].Reason != domain.ProjectImportConflictSamePathDifferentActiveID || plans["rich"].TargetID != "squatter" {
		t.Errorf("rich = %+v", plans["rich"])
	}
	if target.projects["plain"].Path != other {
		t.Error("conflicting target project was overwritten")
	}
	if !rep.HasFailures() {
		t.Error("conflicts must surface as failures")
	}
}

func TestInvalidConfigIsAnErrorForThatProjectOnly(t *testing.T) {
	f := newFixture(t)
	f.project("bad", "bad", "single_repo", `{"agentConfig":{"permissions":"yolo"}}`, false)
	f.project("notjson", "notjson", "single_repo", `[1,2`, false)
	f.project("good", "good", "single_repo", "", false)
	target := newTarget()

	rep, err := Run(context.Background(), target, f.opts())
	if err != nil {
		t.Fatal(err)
	}
	plans := byID(rep)
	if plans["bad"].Action != ActionError || plans["notjson"].Action != ActionError || plans["good"].Action != ActionCreate {
		t.Fatalf("plans = %+v", rep.Projects)
	}
	if len(target.projects) != 1 || rep.Counts.Error != 2 {
		t.Errorf("projects=%v counts=%+v", target.projects, rep.Counts)
	}
}

func TestMissingSourceIsNotAnError(t *testing.T) {
	f := newFixture(t)
	opts := f.opts()
	opts.SourceDataDir = filepath.Join(f.home, "no-such-dir")
	rep, err := Run(context.Background(), newTarget(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || len(rep.Projects) != 0 {
		t.Fatalf("report = %+v", rep)
	}
}

// TestSourceDirIsNeverModified is the safety contract: a run, dry or real, must
// leave the official data dir byte-for-byte and entry-for-entry unchanged.
func TestSourceDirIsNeverModified(t *testing.T) {
	f := newFixture(t)
	seed(f)
	// A live install keeps a WAL next to the database.
	if err := os.WriteFile(filepath.Join(f.dataDir, DatabaseFile+"-wal"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dataDir, "daemon.lock"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := digestTree(t, f.dataDir)

	for _, dry := range []bool{true, false} {
		opts := f.opts()
		opts.DryRun = dry
		if _, err := Run(context.Background(), newTarget(), opts); err != nil {
			t.Fatal(err)
		}
		if after := digestTree(t, f.dataDir); after != before {
			t.Fatalf("source dir changed (dryRun=%v):\nbefore:\n%s\nafter:\n%s", dry, before, after)
		}
	}
	entries, err := os.ReadDir(f.staging)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("snapshot not cleaned up: %v", entries)
	}
}

func digestTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		line := fmt.Sprintf("%s %v %d %d", rel, info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			line += " " + hex.EncodeToString(sum[:8])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestTolerantOfOlderSourceSchema(t *testing.T) {
	// A source with only the bare projects table (no kind/config/workspace_repos/app_settings).
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, DatabaseFile))
	if err != nil {
		t.Fatal(err)
	}
	proj := t.TempDir()
	if _, err := db.Exec(`CREATE TABLE projects (id TEXT PRIMARY KEY, path TEXT NOT NULL);
		INSERT INTO projects VALUES ('only', ?)`, proj); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	target := newTarget()
	rep, err := Run(context.Background(), target, Options{SourceDataDir: dir, StagingDir: filepath.Join(t.TempDir(), "stg")})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Counts.Create != 1 || target.projects["only"].DisplayName != "only" || target.projects["only"].Kind != domain.ProjectKindSingleRepo {
		t.Fatalf("report=%+v target=%+v", rep.Projects, target.projects)
	}
}

func containsSub(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
