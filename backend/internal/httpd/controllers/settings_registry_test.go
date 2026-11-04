package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/settingsreg"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func newRegistryServer(t *testing.T, withValues bool) *httptest.Server {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	engine, err := agentpolicy.New(rules.All(), &policytest.Store{}, agentpolicy.WithManagedFile("", os.ReadFile),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	projects := projectsvc.New(store)
	deps := httpd.APIDeps{Projects: projects}
	if withValues {
		deps.SettingsValues = &settingsview.Service{
			AppSettings:   settingssvc.New(newMemSettings(), nil, settingssvc.Offering{}, nil),
			CleanupPolicy: &auditCleanup{policy: cleanupsvc.DefaultPolicy()},
			AgentPolicy:   engine, Projects: projects,
		}
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func decodeSettingsBody[T any](t *testing.T, body []byte, status int) T {
	t.Helper()
	if status != http.StatusOK {
		t.Fatalf("status %d; %s", status, body)
	}
	var out T
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// B202: the schema route lists the whole registry, in registry order.
func TestSettingsSchemaAPI_ListsTheRegistry(t *testing.T) {
	body, status, _ := doRequest(t, newRegistryServer(t, false), "GET", "/api/v1/settings/schema", "")
	got := decodeSettingsBody[controllers.SettingsSchemaResponse](t, body, status)
	defs := settingsreg.Default().All()
	if len(got.Settings) != len(defs) {
		t.Fatalf("schema has %d settings, registry %d", len(got.Settings), len(defs))
	}
	for i, d := range defs {
		if got.Settings[i].Key != string(d.Key) {
			t.Fatalf("setting %d = %s, want %s", i, got.Settings[i].Key, d.Key)
		}
	}
	byKey := map[string]controllers.SettingDefinition{}
	for _, s := range got.Settings {
		byKey[s.Key] = s
	}
	if r := byKey["app.providerEventRetentionDays"]; r.Kind != "int" || *r.Min != 1 || *r.Max != 3650 || r.Default != float64(14) {
		t.Errorf("retention = %+v", r)
	}
	if r := byKey["project.agentRules"]; !slices.Contains(r.Writers, "orchestrator") || r.MaxBytes == 0 || !r.Nullable {
		t.Errorf("agentRules = %+v", r)
	}
	if r := byKey["project.env"]; !r.Sensitive {
		t.Errorf("env = %+v", r)
	}
	if r := byKey["policy.permissionCap.max"]; !r.MDMLockable || len(r.Env) == 0 {
		t.Errorf("permissionCap.max = %+v", r)
	}
}

// B202: the values route reads app-wide values, or a project's.
func TestSettingsValuesAPI_AppAndProject(t *testing.T) {
	srv := newRegistryServer(t, true)
	repo := gitRepo(t, "valued")
	mustStatus(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"valued","config":{"agentRules":"Be brief.","noCI":true}}`, http.StatusCreated)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/values", "")
	app := decodeSettingsBody[controllers.SettingsValuesResponse](t, body, status)
	appBy := map[string]controllers.SettingValue{}
	for _, v := range app.Values {
		appBy[v.Key] = v
	}
	if v := appBy["app.defaultSessionMode"]; v.Value != "chat" || v.Source != "default" || !slices.Equal(v.Writable, []string{"user"}) {
		t.Errorf("defaultSessionMode = %+v", v)
	}
	if _, ok := appBy["policy.permissionCap.max"]; !ok {
		t.Error("app values miss policy keys")
	}
	if _, ok := appBy["project.agentRules"]; ok || app.Project != "" {
		t.Error("app values include project keys")
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/values?project=valued", "")
	project := decodeSettingsBody[controllers.SettingsValuesResponse](t, body, status)
	by := map[string]controllers.SettingValue{}
	for _, v := range project.Values {
		by[v.Key] = v
	}
	if v := by["project.agentRules"]; v.Value != "Be brief." || v.Source != "project" || !slices.Contains(v.Writable, "orchestrator") {
		t.Errorf("agentRules = %+v", v)
	}
	if v := by["project.noCI"]; v.Value != true || v.Source != "project" {
		t.Errorf("noCI = %+v", v)
	}
	if v := by["project.defaultBranch"]; v.Source != "default" {
		t.Errorf("defaultBranch = %+v", v)
	}
	if project.Project != "valued" {
		t.Errorf("project = %q", project.Project)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/values?project=missing", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

func TestSettingsValuesAPI_NotImplementedWithoutService(t *testing.T) {
	body, status, _ := doRequest(t, newRegistryServer(t, false), "GET", "/api/v1/settings/values", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
