package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
)

// The skills inventory lists the Claude plugins of the account config the
// project's sessions see, and the skills Tallpine installs under its data dir.
func TestProjectSkillsAPI_ListsClaudePluginsAndTallpineSkills(t *testing.T) {
	home, repo, data := t.TempDir(), t.TempDir(), t.TempDir()
	claude := filepath.Join(home, ".claude")
	for p, body := range map[string]string{
		filepath.Join(claude, "plugins", "installed_plugins.json"): `{"version":2,"plugins":{"design@m":[{"scope":"user","installPath":"/x/design","version":"1.2"}]}}`,
		filepath.Join(claude, "settings.json"):                     `{"enabledPlugins":{"design@m":true}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mkSkill(t, filepath.Join(data, "skills"), "using-tallpine", "the CLI")
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{DataDir: data, Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: repo, Config: cfg}}}}).Register(r)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/projects/p/skills", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	var got controllers.ProjectSkillsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := controllers.ProjectPlugin{ID: "design@m", Name: "design", Marketplace: "m", Version: "1.2", Path: "/x/design", Installed: true, AccountEnabled: true}
	if len(got.Plugins) == 1 {
		got.Plugins[0].Roles = nil // checked in TestProjectSkillsAPI_PerRoleValuesAndEffects
	}
	if len(got.Plugins) != 1 || !reflect.DeepEqual(got.Plugins[0], want) {
		t.Fatalf("plugins = %+v", got.Plugins)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "using-tallpine" || got.Skills[0].Scope != "tallpine" {
		t.Fatalf("skills = %+v", got.Skills)
	}
}
