package controllers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

type appSkillDefaults struct{ roles domain.SkillRoles }

func (a appSkillDefaults) WorkflowDefaults(context.Context) domain.WorkflowDefaults {
	return domain.WorkflowDefaults{AppInstructions: domain.AppInstructions{SkillRoles: a.roles}}
}

func appSkillsRouter(t *testing.T, dataDir string, project, app domain.SkillRoles) chi.Router {
	t.Helper()
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": ""}, SkillRoles: project}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{DataDir: dataDir, AppDefaults: appSkillDefaults{app},
		Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: t.TempDir(), Config: cfg}}}}).Register(r)
	return r
}

func roleOf(t *testing.T, skills []controllers.ProjectSkill, name, role string) controllers.ProjectSkillRole {
	t.Helper()
	for _, s := range skills {
		if s.Name != name {
			continue
		}
		for _, r := range s.Roles {
			if r.Role == role {
				return r
			}
		}
	}
	t.Fatalf("no %s row for %s in %+v", role, name, skills)
	return controllers.ProjectSkillRole{}
}

// App custom skills are created on the app page and listed on every
// project's page; the project page shows which switches follow the app
// default, a project switch wins, and an unset project switch (reset)
// returns to the app's value.
func TestAppCustomSkillsAndInheritedSwitchesOnProjectPage(t *testing.T) {
	dataDir := t.TempDir()
	app := domain.SkillRoles{"reviewer": {"house": true}, "worker": {"house": false}}
	project := domain.SkillRoles{"worker": {"house": true}}
	r := appSkillsRouter(t, dataDir, project, app)
	if rec := customCall(r, http.MethodPost, "/settings/skills/custom", `{"name":"house","content":"---\ndescription: House rules\n---\n"}`, false); rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(skillbridge.AppCustomDir(dataDir) + "/house/SKILL.md"); err != nil {
		t.Fatalf("app custom skill not under skills-custom: %v", err)
	}

	for _, id := range []string{"p", "other"} {
		rec := customCall(r, http.MethodGet, "/projects/"+id+"/skills", "", false)
		var inv controllers.ProjectSkillsResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &inv)
		var conv string
		for _, s := range inv.Skills {
			if s.Name == "house" {
				conv = s.Convention
			}
		}
		if conv != "custom-app" {
			t.Fatalf("project %s: app custom skill missing (convention %q): %s", id, conv, rec.Body)
		}
		if got := roleOf(t, inv.Skills, "house", "reviewer"); !got.On || got.Set || !got.App || !got.Default {
			t.Fatalf("reviewer row does not follow the app default: %+v", got)
		}
		if got := roleOf(t, inv.Skills, "house", "worker"); !got.On || !got.Set || !got.App || got.Default {
			t.Fatalf("worker row: project override must win over app off, reset to app off: %+v", got)
		}
		if got := roleOf(t, inv.Skills, "house", "orchestrator"); !got.On || got.Set || got.App {
			t.Fatalf("orchestrator row follows the built-in default: %+v", got)
		}
	}

	rec := customCall(r, http.MethodGet, "/settings/skills", "", false)
	var appInv controllers.AppSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &appInv); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("app skills %d %s", rec.Code, rec.Body)
	}
	if got := roleOf(t, appInv.Skills, "house", "reviewer"); !got.On || !got.Set || got.App {
		t.Fatalf("app page reviewer row: %+v", got)
	}
	if got := roleOf(t, appInv.Skills, "house", "worker"); got.On || !got.Set {
		t.Fatalf("app page worker row: %+v", got)
	}

	if rec := customCall(r, http.MethodPut, "/settings/skills/custom/house", `{"content":"new"}`, false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"new"`) {
		t.Fatalf("update %d %s", rec.Code, rec.Body)
	}
	if rec := customCall(r, http.MethodDelete, "/settings/skills/custom/house", "", false); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", rec.Code, rec.Body)
	}
}

// No agent token may create, edit or delete an app custom skill.
func TestAgentsCannotWriteAppCustomSkills(t *testing.T) {
	r := appSkillsRouter(t, t.TempDir(), nil, nil)
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/settings/skills/custom", `{"name":"x","content":"y"}`},
		{http.MethodPost, "/settings/skills/custom/import", `{"name":"x","source":"/tmp"}`},
		{http.MethodPut, "/settings/skills/custom/x", `{"content":"y"}`},
		{http.MethodDelete, "/settings/skills/custom/x", ""},
	} {
		if rec := customCall(r, c.method, c.path, c.body, true); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s by an agent: %d", c.method, c.path, rec.Code)
		}
	}
}
