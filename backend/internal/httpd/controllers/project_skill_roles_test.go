package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// Each skill and plugin carries its per-role switch (set or the role
// default, reviewers off) and the effect of the switch on every harness.
func TestProjectSkillsAPI_PerRoleValuesAndEffects(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"enabledPlugins":{"design@m":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	mkSkill(t, filepath.Join(repo, ".claude", "skills"), "repo-a", "repo")
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""}, SkillRoles: domain.SkillRoles{"worker": {"repo-a": false}}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Skills) != 1 || len(got.Plugins) != 1 {
		t.Fatalf("%d %s (%v)", rec.Code, rec.Body, err)
	}
	role := func(roles []controllers.ProjectSkillRole, name string) controllers.ProjectSkillRole {
		for _, r := range roles {
			if r.Role == name {
				return r
			}
		}
		t.Fatalf("no %s role in %+v", name, roles)
		return controllers.ProjectSkillRole{}
	}
	effect := func(r controllers.ProjectSkillRole, harness string) string {
		for _, e := range r.Effects {
			if e.Harness == harness {
				return e.Effect
			}
		}
		return ""
	}
	skill := got.Skills[0].Roles
	if w := role(skill, "worker"); w.On || !w.Set || !w.Default || effect(w, "claude-code") != "enforced" || effect(w, "deepseek-harness") != "advisory" {
		t.Fatalf("worker = %+v", w)
	}
	if o := role(skill, "orchestrator"); !o.On || o.Set {
		t.Fatalf("orchestrator = %+v", o)
	}
	if rv := role(skill, "reviewer"); rv.On || rv.Default || effect(rv, "claude-code") != "unsupported" {
		t.Fatalf("reviewer repo skill = %+v", rv)
	}
	plugin := got.Plugins[0].Roles
	if w, rv := role(plugin, "worker"), role(plugin, "reviewer"); !w.On || rv.On || rv.Default || effect(rv, "claude-code") != "enforced" || effect(rv, "codex") != "unsupported" {
		t.Fatalf("plugin roles = %+v / %+v", w, rv)
	}
}

// Reviewer and orchestrator skill switches are the user's alone (B740 a5):
// the project's own orchestrator may switch worker skills, never theirs.
func TestSkillRolesReviewerAndOrchestratorAreHumanOnly(t *testing.T) {
	s := newWriteServer(t, "", nil)
	repo := gitRepo(t, "own")
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"own"}`, http.StatusCreated)
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})

	if _, body, status := orch.patch(t, `{"project":"own","set":{"project.skillRoles":{"worker":{"design":false}}}}`); status != http.StatusOK {
		t.Fatalf("orchestrator worker switch = %d; %s", status, body)
	}
	for _, role := range []string{"reviewer", "orchestrator"} {
		for name, req := range map[string]string{
			"object":     `{"project":"own","set":{"project.skillRoles":{"worker":{"design":false},"` + role + `":{"design":true}}}}`,
			"nested key": `{"project":"own","set":{"project.skillRoles.` + role + `":{"design":true}}}`,
		} {
			_, body, status := orch.patch(t, req)
			assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
			if !strings.Contains(string(body), "project.skillRoles."+role) {
				t.Errorf("%s %s: error does not name the key: %s", role, name, body)
			}
		}
		raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"skillRoles":{"`+role+`":{"design":true}}}}`)
		if status != http.StatusForbidden {
			t.Fatalf("config route %s switch = %d; %s", role, status, raw)
		}
	}
	rec, _, err := s.store.GetProject(context.Background(), "own")
	if err != nil || len(rec.Config.SkillRoles["reviewer"]) != 0 || len(rec.Config.SkillRoles["orchestrator"]) != 0 || rec.Config.SkillRoles["worker"]["design"] {
		t.Fatalf("stored skillRoles = %+v (%v)", rec.Config.SkillRoles, err)
	}
	if _, body, status := s.patch(t, `{"project":"own","set":{"project.skillRoles":{"worker":{"design":false},"reviewer":{"design":true},"orchestrator":{"x":false}}}}`); status != http.StatusOK {
		t.Fatalf("user switches = %d; %s", status, body)
	}
}

// Each skill carries its master and agent-type switches: unset is on, the
// project's own value wins, and a project reset returns to the app's.
func TestProjectSkillsAPI_AccessSwitches(t *testing.T) {
	repo := t.TempDir()
	mkSkill(t, filepath.Join(repo, ".claude", "skills"), "repo-a", "repo")
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": ""}, SkillRoles: domain.SkillRoles{
		domain.SkillMasterKey: {"repo-a": false}, domain.SkillAgentKey("codex"): {"repo-a": false},
	}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || len(got.Skills) != 1 {
		t.Fatalf("%d %s (%v)", rec.Code, rec.Body, err)
	}
	byKey := map[string]controllers.ProjectSkillAccess{}
	for _, a := range got.Skills[0].Access {
		byKey[a.Key] = a
	}
	if len(byKey) != 5 || byKey["all"].On || !byKey["all"].Set || byKey["agentCodex"].On || !byKey["agentKiro"].On || byKey["agentKiro"].Set {
		t.Fatalf("access = %+v", got.Skills[0].Access)
	}
}
