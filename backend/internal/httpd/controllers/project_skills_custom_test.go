package controllers_test

import (
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

func customRouter(t *testing.T, dataDir string) chi.Router {
	t.Helper()
	cfg := &domain.ProjectConfig{Env: map[string]string{"HOME": t.TempDir(), "CLAUDE_CONFIG_DIR": ""}}
	r := chi.NewRouter()
	(&controllers.ProjectSkillsController{DataDir: dataDir, Projects: skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: t.TempDir(), Config: cfg}}}}).Register(r)
	return r
}

func customCall(r chi.Router, method, path, body string, agent bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if agent {
		req = req.WithContext(requestscope.WithAgent(req.Context(), requestscope.Agent{Role: "orchestrator", SessionID: "p-1", ProjectID: "p"}))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestProjectCustomSkillsCRUD(t *testing.T) {
	dataDir := t.TempDir()
	r := customRouter(t, dataDir)
	rec := customCall(r, http.MethodPost, "/projects/p/skills/custom", `{"name":"sql","content":"---\ndescription: SQL checks\n---\nbody"}`, false)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d %s", rec.Code, rec.Body)
	}
	if rec := customCall(r, http.MethodPost, "/projects/p/skills/custom", `{"name":"sql","content":"x"}`, false); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate %d", rec.Code)
	}
	if rec := customCall(r, http.MethodPost, "/projects/p/skills/custom", `{"name":"../evil","content":"x"}`, false); rec.Code != http.StatusBadRequest {
		t.Fatalf("escaping name %d", rec.Code)
	}
	if rec := customCall(r, http.MethodPut, "/projects/p/skills/custom/sql", `{"content":"new"}`, false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"new"`) {
		t.Fatalf("update %d %s", rec.Code, rec.Body)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("imported"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(controllers.ImportProjectCustomSkillRequest{Name: "imp", Source: src})
	rec = customCall(r, http.MethodPost, "/projects/p/skills/custom/import", string(body), false)
	var imp controllers.ProjectCustomSkill
	_ = json.Unmarshal(rec.Body.Bytes(), &imp)
	if rec.Code != http.StatusCreated || len(imp.Skipped) != 1 || !strings.Contains(imp.Skipped[0], "run.sh") {
		t.Fatalf("import %d %s", rec.Code, rec.Body)
	}

	rec = customCall(r, http.MethodGet, "/projects/p/skills", "", false)
	var inv controllers.ProjectSkillsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &inv)
	var rows []string
	for _, s := range inv.Skills {
		if s.Scope == "custom" {
			rows = append(rows, s.Name)
			for _, role := range s.Roles {
				if role.Role == domain.RoleReviewer && (role.On || role.Default) {
					t.Fatalf("custom skill on for the reviewer by default: %+v", role)
				}
			}
		}
	}
	if strings.Join(rows, ",") != "imp,sql" {
		t.Fatalf("custom rows %v (%s)", rows, rec.Body)
	}

	if rec := customCall(r, http.MethodDelete, "/projects/p/skills/custom/sql", "", false); rec.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", rec.Code, rec.Body)
	}
	if rec := customCall(r, http.MethodGet, "/projects/p/skills/custom/sql", "", false); rec.Code != http.StatusNotFound {
		t.Fatalf("get deleted %d", rec.Code)
	}
}

// No agent token may author a custom skill: one switched on for the reviewer
// is part of its task.
func TestProjectCustomSkillsRefuseAgents(t *testing.T) {
	dataDir := t.TempDir()
	r := customRouter(t, dataDir)
	if rec := customCall(r, http.MethodPost, "/projects/p/skills/custom", `{"name":"seed","content":"x"}`, false); rec.Code != http.StatusCreated {
		t.Fatalf("seed %d", rec.Code)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodPost, "/projects/p/skills/custom", `{"name":"a","content":"x"}`},
		{http.MethodPost, "/projects/p/skills/custom/import", `{"name":"a","source":"/tmp"}`},
		{http.MethodPut, "/projects/p/skills/custom/seed", `{"content":"evil"}`},
		{http.MethodDelete, "/projects/p/skills/custom/seed", ""},
	} {
		if rec := customCall(r, c.method, c.path, c.body, true); rec.Code != http.StatusForbidden {
			t.Errorf("agent %s %s = %d", c.method, c.path, rec.Code)
		}
	}
	if rec := customCall(r, http.MethodGet, "/projects/p/skills/custom/seed", "", true); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"content":"x"`) {
		t.Fatalf("agent read %d %s", rec.Code, rec.Body)
	}
}
