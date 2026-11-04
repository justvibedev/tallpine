package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

type skillsProjects struct{ res projectsvc.GetResult }

func (p skillsProjects) Get(context.Context, domain.ProjectID) (projectsvc.GetResult, error) {
	return p.res, nil
}

func mkSkill(t *testing.T, root, name, desc string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + desc + "\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestProjectSkillsAPI_ListsEveryConventionWithHarnessVisibility(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	mkSkill(t, filepath.Join(repo, ".agents", "skills"), "graphify", "graph things")
	mkSkill(t, filepath.Join(home, ".agents", "skills"), "cloudflare", "cf")
	mkSkill(t, filepath.Join(home, ".claude", "skills"), "graphify", "user copy")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	status := func(skillIdx int, harness string) string {
		for _, h := range got.Skills[skillIdx].Harnesses {
			if h.Harness == harness {
				return h.Status
			}
		}
		return "missing"
	}
	if len(got.Skills) != 3 || !got.AgentsMd || got.ClaudeMd {
		t.Fatalf("response = %+v", got)
	}
	// Sorted by name: cloudflare, graphify (repo .agents), graphify (user .claude).
	if got.Skills[0].Name != "cloudflare" || got.Skills[0].Description != "cf" {
		t.Errorf("skill 0 = %+v", got.Skills[0])
	}
	if s := status(0, "claude-code"); s != "missing" {
		t.Errorf("cloudflare/claude = %s", s)
	}
	if s := status(0, "codex"); s != "native" {
		t.Errorf("cloudflare/codex = %s", s)
	}
	if s := status(0, "kiro"); s != "unverified" {
		t.Errorf("cloudflare/kiro = %s", s)
	}
	// Claude has its own graphify; the repo one is shadowed for it, native for Codex.
	for i := 1; i <= 2; i++ {
		repoCopy := got.Skills[i].Scope == "repo"
		wantClaude, wantCodex := "native", "shadowed"
		if repoCopy {
			wantClaude, wantCodex = "shadowed", "native"
		}
		if status(i, "claude-code") != wantClaude || status(i, "codex") != wantCodex {
			t.Errorf("graphify %s: claude=%s codex=%s", got.Skills[i].Scope, status(i, "claude-code"), status(i, "codex"))
		}
	}
	if len(got.Collisions) != 1 || got.Collisions[0].Name != "graphify" {
		t.Errorf("collisions = %+v", got.Collisions)
	}
	if got.Instructions[0].Harness != "claude-code" || got.Instructions[0].Status != "missing" {
		t.Errorf("instructions = %+v", got.Instructions)
	}
}

func TestProjectSkillsAPI_OfferListedThenAppliedOnceWithoutOverwriting(t *testing.T) {
	repo := t.TempDir()
	mkSkill(t, filepath.Join(repo, ".codex", "skills"), "source-regression-qa", "QA")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}, Env: map[string]string{"HOME": t.TempDir()}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
		return rec
	}

	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(do(http.MethodGet, "/projects/p/skills").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Offers) != 2 || got.Offers[0].ID != "claude-md" || got.Offers[1].Items[0].Path != ".claude/skills/source-regression-qa" {
		t.Fatalf("offers = %+v", got.Offers)
	}
	if _, err := os.Lstat(filepath.Join(repo, "CLAUDE.md")); err == nil {
		t.Fatal("GET must not write")
	}

	rec := do(http.MethodPost, "/projects/p/skills/offers/claude-md/apply")
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "CLAUDE.md")); string(b) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q", b)
	}
	if rec := do(http.MethodPost, "/projects/p/skills/offers/claude-md/apply"); rec.Code != http.StatusNotFound {
		t.Errorf("second apply = %d, want 404", rec.Code)
	}
	if rec := do(http.MethodPost, "/projects/p/skills/offers/claude-skills/apply"); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if _, err := os.ReadFile(filepath.Join(repo, ".claude", "skills", "source-regression-qa", "SKILL.md")); err != nil {
		t.Errorf("skill not visible to Claude: %v", err)
	}
}

func TestProjectSkillsAPI_OffersEnabledFollowsProjectConfig(t *testing.T) {
	repo := t.TempDir()
	for _, off := range []bool{false, true} {
		cfg := &domain.ProjectConfig{SkillsOfferOff: off, Env: map[string]string{"HOME": t.TempDir()}}
		r := chi.NewRouter()
		(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
		var got controllers.ProjectSkillsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.OffersEnabled == off {
			t.Errorf("SkillsOfferOff=%v: offersEnabled = %v", off, got.OffersEnabled)
		}
	}
}

// After an offer is applied to the main checkout the files are untracked, so
// sessions (worktrees of a commit) still lack them: the page must say pending,
// not "sees it", and must keep listing them until they are committed.
func TestProjectSkillsAPI_AppliedOfferIsPendingCommit(t *testing.T) {
	repo := t.TempDir()
	gitIn := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.email=t@t", "-c", "user.name=t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	gitIn("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "init")

	cfg := &domain.ProjectConfig{Worker: domain.RoleOverride{Harness: domain.HarnessClaudeCode}, Env: map[string]string{"HOME": t.TempDir()}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	get := func() controllers.ProjectSkillsResponse {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
		var got controllers.ProjectSkillsResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := get(); len(got.PendingCommit) != 0 {
		t.Fatalf("clean checkout reports pending: %v", got.PendingCommit)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/projects/p/skills/offers/claude-md/apply", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	got := get()
	if len(got.Offers) != 0 || got.Instructions[0].Status != "pending" || len(got.PendingCommit) != 1 || got.PendingCommit[0] != "CLAUDE.md" {
		t.Fatalf("after apply: offers=%v instructions=%v pending=%v", got.Offers, got.Instructions, got.PendingCommit)
	}
	gitIn("add", "-A")
	gitIn("commit", "-q", "-m", "claude")
	if got := get(); got.Instructions[0].Status != "native" || len(got.PendingCommit) != 0 {
		t.Fatalf("after commit: %v %v", got.Instructions, got.PendingCommit)
	}
}

func TestProjectSkillsAPI_DeselectedSkillsAreHiddenFromOffersAndRemovableLinksFlagged(t *testing.T) {
	repo := t.TempDir()
	mkSkill(t, filepath.Join(repo, ".agents", "skills"), "keep", "k")
	mkSkill(t, filepath.Join(repo, ".agents", "skills"), "drop", "d")
	if err := os.WriteFile(filepath.Join(repo, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude", "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../.agents/skills/keep", filepath.Join(repo, ".claude", "skills", "keep")); err != nil {
		t.Fatal(err)
	}
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": ""}, SkillsDeselected: []string{"drop"}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(do(http.MethodGet, "/projects/p/skills", "").Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, o := range got.Offers {
		for _, it := range o.Items {
			if strings.HasSuffix(it.Path, "/drop") {
				t.Fatalf("deselected skill offered: %+v", o)
			}
		}
	}
	flags := map[string]controllers.ProjectSkill{}
	for _, s := range got.Skills {
		flags[s.Convention+":"+s.Name] = s
	}
	if !flags["agents:drop"].Deselected || flags["agents:keep"].Deselected {
		t.Fatalf("deselected flags wrong: %+v", flags)
	}
	if !flags["claude:keep"].Removable || flags["agents:keep"].Removable {
		t.Fatalf("removable flags wrong: %+v", flags)
	}
	if rec := do(http.MethodPost, "/projects/p/skills/remove", `{"path":".agents/skills/keep"}`); rec.Code != http.StatusConflict {
		t.Fatalf("removing a source: %d %s", rec.Code, rec.Body)
	}
	if rec := do(http.MethodPost, "/projects/p/skills/remove", `{"path":".claude/skills/keep"}`); rec.Code != http.StatusOK {
		t.Fatalf("remove link: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".claude", "skills", "keep")); !os.IsNotExist(err) {
		t.Fatal("link not removed")
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents", "skills", "keep", "SKILL.md")); err != nil {
		t.Fatal("source skill damaged")
	}
	if rec := do(http.MethodPost, "/projects/p/skills/remove", `{"path":".claude/skills/keep"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("remove again: %d", rec.Code)
	}
}
