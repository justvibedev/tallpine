package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/service/settingsview"
	"tallpine.eu/tallpine/backend/internal/service/settingswrite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type writeServer struct {
	srv    *httptest.Server
	store  *sqlite.Store
	router http.Handler
}

// as serves the same daemon to requests carrying agent's principal, as the
// agent scope gate leaves it for handlers.
func (s writeServer) as(t *testing.T, agent requestscope.Agent) writeServer {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.router.ServeHTTP(w, r.WithContext(requestscope.WithAgent(r.Context(), agent)))
	}))
	t.Cleanup(srv.Close)
	return writeServer{srv: srv, store: s.store, router: s.router}
}

func newWriteServer(t *testing.T, managedJSON string, env map[string]string) writeServer {
	t.Helper()
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	managed := ""
	if managedJSON != "" {
		managed = filepath.Join(t.TempDir(), "policy.json")
		if err := os.WriteFile(managed, []byte(managedJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	engine, err := agentpolicy.New(rules.All(), &policytest.Store{}, agentpolicy.WithManagedFile(managed, os.ReadFile),
		agentpolicy.WithEnv(func(string) (string, bool) { return "", false }))
	if err != nil {
		t.Fatal(err)
	}
	audit := settingsaudit.New(store, log)
	projects := projectsvc.New(store)
	projects.SetSettingsAudit(audit)
	app := settingssvc.New(newMemSettings(), nil, settingssvc.Offering{}, nil)
	cleanup := &auditCleanup{policy: cleanupsvc.DefaultPolicy()}
	view := &settingsview.Service{AppSettings: app, CleanupPolicy: cleanup, AgentPolicy: engine, Projects: projects,
		LookupEnv: func(name string) (string, bool) { v, ok := env[name]; return v, ok }}
	router := httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Projects: projects, SettingsValues: view, SettingsChanges: store, AgentPolicy: engine,
		SettingsWriter: &settingswrite.Writer{View: view, Projects: projects, AppSettings: app, CleanupPolicy: cleanup, AgentPolicy: engine, Audit: audit},
	}, httpd.ControlDeps{})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)
	return writeServer{srv: srv, store: store, router: router}
}

func (s writeServer) patch(t *testing.T, body string) (map[string]controllers.SettingValue, []byte, int) {
	t.Helper()
	raw, status, _ := doRequest(t, s.srv, "PATCH", "/api/v1/settings/values", body)
	if status != http.StatusOK {
		return nil, raw, status
	}
	var resp controllers.SettingsValuesResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	out := map[string]controllers.SettingValue{}
	for _, v := range resp.Values {
		out[v.Key] = v
	}
	return out, raw, status
}

// B202: app, cleanup and agent policy settings change by key in one request,
// the response holds the new effective values and the change log the actor.
func TestSettingsWriteAPI_AppCleanupAndPolicy(t *testing.T) {
	s := newWriteServer(t, "", nil)
	got, body, status := s.patch(t, `{"set":{"app.versionRefresh":false,"app.chatHibernation.workerIdleSeconds":600,
		"app.providerEventRetentionDays":30,"app.cleanup.automatic":false,"policy.permissionCap.max":"auto",
		"app.harnessDefaults":{"claude-code":{"autoCompactWindow":"1m"}}}}`)
	if status != http.StatusOK {
		t.Fatalf("patch = %d; %s", status, body)
	}
	for key, want := range map[string]any{
		"app.versionRefresh": false, "app.chatHibernation.workerIdleSeconds": float64(600),
		"app.providerEventRetentionDays": float64(30), "app.cleanup.automatic": false, "policy.permissionCap.max": "auto",
	} {
		if got[key].Value != want {
			t.Errorf("%s = %v, want %v", key, got[key].Value, want)
		}
	}
	if hd, _ := json.Marshal(got["app.harnessDefaults"].Value); !strings.Contains(string(hd), `"autoCompactWindow":"1m"`) {
		t.Errorf("harnessDefaults = %s", hd)
	}
	if got["app.chatHibernation.orchestratorIdleSeconds"].Value != float64(3600) {
		t.Errorf("an untouched sibling changed: %v", got["app.chatHibernation.orchestratorIdleSeconds"])
	}
	rows, err := s.store.ListSettingsChanges(context.Background(), "", 50)
	if err != nil {
		t.Fatal(err)
	}
	logged := map[string]string{}
	for _, r := range rows {
		logged[r.Key] = r.Actor
	}
	for _, k := range []string{"app.versionRefresh", "app.cleanup.automatic", "policy.permissionCap.max", "app.providerEventRetentionDays"} {
		if logged[k] != "user" {
			t.Errorf("%s not in the change log: %v", k, logged)
		}
	}

	got, _, status = s.patch(t, `{"unset":["app.versionRefresh","app.cleanup.automatic"]}`)
	if status != http.StatusOK || got["app.versionRefresh"].Value != true || got["app.cleanup.automatic"].Value != true {
		t.Fatalf("unset app = %d %+v", status, got)
	}
}

// B202: project settings, including nested keys, change through the project
// config; unset clears a field.
func TestSettingsWriteAPI_Project(t *testing.T) {
	s := newWriteServer(t, "", nil)
	repo := gitRepo(t, "written")
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"written","config":{"noCI":true,"postCreate":["make"]}}`, http.StatusCreated)
	got, body, status := s.patch(t, `{"project":"written","set":{"project.agentRules":"Be brief.","project.orchestrator.autoCompactWindow":"1m","policy.permissionCap.max":"auto"},"unset":["project.noCI"]}`)
	if status != http.StatusOK {
		t.Fatalf("patch = %d; %s", status, body)
	}
	if got["project.agentRules"].Value != "Be brief." || got["project.orchestrator.autoCompactWindow"].Value != "1m" ||
		got["project.noCI"].Source != "default" || got["policy.permissionCap.max"].Value != "auto" {
		t.Fatalf("values = %+v", got)
	}
	raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/projects/written", "")
	if !strings.Contains(string(raw), `"make"`) {
		t.Fatalf("an untouched project field was lost: %s", raw)
	}
	rows, _ := s.store.ListSettingsChanges(context.Background(), "written", 50)
	if len(rows) == 0 {
		t.Fatal("project change not logged")
	}
}

// settings unset policy.* drops the stored value, so the default applies again.
func TestSettingsWriteAPI_UnsetPolicyResetsToDefault(t *testing.T) {
	s := newWriteServer(t, "", nil)
	if _, body, status := s.patch(t, `{"set":{"policy.securityMode.enabled":true}}`); status != http.StatusOK || !strings.Contains(string(body), `"key":"policy.securityMode.enabled","value":true,"source":"user"`) {
		t.Fatalf("set: %d %s", status, body)
	}
	_, body, status := s.patch(t, `{"unset":["policy.securityMode.enabled"]}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"key":"policy.securityMode.enabled","value":false,"source":"default"`) {
		t.Fatalf("unset: %d %s", status, body)
	}
}

// B202: a change is validated as a whole; nothing is written when any key is
// refused.
func TestSettingsWriteAPI_RefusesWithoutWriting(t *testing.T) {
	s := newWriteServer(t, "", nil)
	repo := gitRepo(t, "refused")
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"refused"}`, http.StatusCreated)
	for _, c := range []struct{ body, key string }{
		{`{"set":{"app.versionRefresh":false,"app.providerEventRetentionDays":0}}`, "app.providerEventRetentionDays"},
		{`{"set":{"project.noCI":true}}`, "project.noCI"},
		{`{"project":"refused","set":{"app.versionRefresh":false}}`, "app.versionRefresh"},
		{`{"set":{"app.nope":1}}`, "app.nope"},
		{`{"set":{"app.versionRefresh":false,"app.mobile.enabled":false}}`, "app.mobile.enabled"},
		{`{"set":{"app.versionRefresh":false},"unset":["app.versionRefresh"]}`, "app.versionRefresh"},
		{`{"project":"refused","set":{"project.agentConfig":{"permissionz":"auto"}}}`, "project.agentConfig"},
	} {
		_, body, status := s.patch(t, c.body)
		assertErrorCode(t, body, status, http.StatusBadRequest, "SETTINGS_INVALID")
		if !strings.Contains(string(body), c.key) {
			t.Errorf("%s: error does not name %s: %s", c.body, c.key, body)
		}
	}
	_, body, status := s.patch(t, `{}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "SETTINGS_INVALID")
	_, body, status = s.patch(t, `{"set":{},"bogus":1}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/settings/values", "")
	if strings.Contains(string(raw), `"key":"app.versionRefresh","value":false`) {
		t.Fatal("a refused change was partly written")
	}
	_, body, status = s.patch(t, `{"project":"missing","set":{"project.noCI":true}}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

// B411: app.reviewDefaults is retired; a write is refused with a pointer to the
// project keys that replaced it, and nothing is written.
func TestSettingsWriteAPI_RetiredReviewDefaults(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, body := range []string{
		`{"set":{"app.reviewDefaults":{"autoReview":true}}}`,
		`{"unset":["app.reviewDefaults"]}`,
		`{"set":{"app.versionRefresh":false,"app.reviewDefaults":{}}}`,
	} {
		_, raw, status := s.patch(t, body)
		assertErrorCode(t, raw, status, http.StatusBadRequest, "SETTING_RETIRED")
		if !strings.Contains(string(raw), "project.reviewRisk") {
			t.Errorf("%s: no pointer to the project keys: %s", body, raw)
		}
	}
	raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/settings/values", "")
	if strings.Contains(string(raw), `"key":"app.versionRefresh","value":false`) || strings.Contains(string(raw), "app.reviewDefaults") {
		t.Fatalf("retired write leaked or the key is still served: %s", raw)
	}
}

// B202: managed config and a forcing environment variable lock their keys.
func TestSettingsWriteAPI_LockedKeys(t *testing.T) {
	s := newWriteServer(t, `{"schemaVersion":2,"rules":{"permissionCap":{"max":"auto"}}}`, map[string]string{"TALLPINE_VERSION_REFRESH": "off"})
	for _, body := range []string{
		`{"set":{"policy.permissionCap.max":"bypass-permissions"}}`,
		`{"set":{"app.versionRefresh":true}}`,
	} {
		_, raw, status := s.patch(t, body)
		assertErrorCode(t, raw, status, http.StatusConflict, "SETTING_LOCKED")
	}
}

func TestSettingsWriteAPI_NotImplementedWithoutWriter(t *testing.T) {
	_, body, status := writeServer{srv: newRegistryServer(t, true)}.patch(t, `{"set":{"app.versionRefresh":false}}`)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// B202 permission model: an orchestrator changes any setting of its own
// project and tightens (never loosens) its agent policy; app settings, the
// install-wide policy, other projects and workers are refused with
// AGENT_NOT_PERMITTED and nothing is written. Agents see project.env redacted.
func TestSettingsWriteAPI_AgentPermissions(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, id := range []string{"own", "other"} {
		repo := gitRepo(t, id)
		mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"`+id+`","config":{"env":{"TOKEN":"s3cret"}}}`, http.StatusCreated)
	}
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})
	worker := s.as(t, requestscope.Agent{Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"})

	got, body, status := orch.patch(t, `{"project":"own","set":{"project.noCI":true,"policy.permissionCap.max":"auto"}}`)
	if status != http.StatusOK {
		t.Fatalf("orchestrator own project = %d; %s", status, body)
	}
	if got["project.noCI"].Value != true || got["policy.permissionCap.max"].Value != "auto" || got["project.env"].Value != "<redacted>" {
		t.Fatalf("values = %+v", got)
	}
	rows, _ := s.store.ListSettingsChanges(context.Background(), "own", 50)
	if len(rows) == 0 || rows[0].Actor != "orchestrator:own-orch" {
		t.Fatalf("change log = %+v", rows)
	}

	for _, c := range []struct{ name, body, key string }{
		{"loosen policy", `{"project":"own","set":{"policy.permissionCap.max":"none"}}`, "policy.permissionCap.max"},
		{"user-only policy", `{"project":"own","set":{"policy.crossProjectOrchestration.enabled":true}}`, "policy.crossProjectOrchestration.enabled"},
		{"other project", `{"project":"other","set":{"project.noCI":true}}`, "own"},
		{"app setting", `{"set":{"app.versionRefresh":false}}`, "own"},
		// project.env reads redacted, so an agent may not drop or change an
		// entry it cannot see (the change log redacts both sides).
		{"replace env", `{"project":"own","set":{"project.env":{"A":"b"}}}`, "TOKEN"},
		{"change env", `{"project":"own","set":{"project.env":{"TOKEN":"x","A":"b"}}}`, "TOKEN"},
		{"unset env", `{"project":"own","unset":["project.env"]}`, "TOKEN"},
	} {
		_, body, status := orch.patch(t, c.body)
		assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
		if !strings.Contains(string(body), c.key) {
			t.Errorf("%s: error does not name %s: %s", c.name, c.key, body)
		}
	}
	_, body, status = worker.patch(t, `{"project":"own","set":{"project.noCI":false}}`)
	assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")

	raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/settings/values?project=other", "")
	if !strings.Contains(string(raw), `"s3cret"`) || strings.Contains(string(raw), `"key":"project.noCI","value":true`) {
		t.Fatalf("the user's view of the other project changed or lost its env: %s", raw)
	}
	raw, _, _ = doRequest(t, s.srv, "GET", "/api/v1/settings/values?project=own", "")
	if !strings.Contains(string(raw), `"key":"policy.permissionCap.max","value":"auto"`) || !strings.Contains(string(raw), `"TOKEN":"s3cret"`) {
		t.Fatalf("a refused loosening was written: %s", raw)
	}

	raw, status, _ = doRequest(t, worker.srv, "GET", "/api/v1/settings/values?project=own", "")
	if status != http.StatusOK || strings.Contains(string(raw), "s3cret") ||
		!strings.Contains(string(raw), `"key":"project.env","value":"\u003credacted\u003e"`) && !strings.Contains(string(raw), `"key":"project.env","value":"<redacted>"`) {
		t.Fatalf("worker read of own project = %d %s", status, raw)
	}
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "fresh"))+`,"projectId":"fresh"}`, http.StatusCreated)
	fresh := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "fresh-orch", ProjectID: "fresh"})
	if _, body, status := fresh.patch(t, `{"project":"fresh","set":{"project.env":{"A":"b"}}}`); status != http.StatusOK {
		t.Fatalf("orchestrator adding env to an empty map = %d; %s", status, body)
	}
	for _, path := range []string{"/api/v1/settings/values?project=other", "/api/v1/settings/changes?project=other"} {
		raw, status, _ := doRequest(t, worker.srv, "GET", path, "")
		assertErrorCode(t, raw, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
	}
	if _, status, _ := doRequest(t, worker.srv, "GET", "/api/v1/settings/changes?project=own", ""); status != http.StatusOK {
		t.Fatalf("worker read of own change log = %d", status)
	}
}

// B202 PR8: the managed agentSettings rule is the organization's off switch.
// deny refuses every agent setting change on every agent path (PATCH
// /settings/values and PUT /projects/{id}/rules), a deny list refuses the keys
// it names, allowList admits only the listed ones; the user is never bound,
// and agents cannot change the rule itself.
func TestSettingsWriteAPI_ManagedAgentSettings(t *testing.T) {
	for _, c := range []struct {
		name, rule string
		allowed    []string // keys an orchestrator may still set
		refused    []string
	}{
		{"deny", `{"mode":"deny"}`, nil, []string{"project.noCI", "project.agentRules"}},
		{"deny list", `{"deny":["project.postCreate","project.orchestrator"]}`, []string{"project.noCI"}, []string{"project.postCreate", "project.orchestrator.idleCompactAt"}},
		{"allowList", `{"mode":"allowList","allow":["project.agentRules"]}`, []string{"project.agentRules"}, []string{"project.noCI"}},
		// A typo fails closed: an invalid list or mode turns agent writes off.
		{"bad pattern", `{"deny":["env"]}`, nil, []string{"project.agentRules"}},
		{"bad mode", `{"mode":"sometimes"}`, nil, []string{"project.agentRules"}},
		// An explicit mode is kept by the engine's fallback; the rule itself
		// still turns writes off.
		{"bad pattern explicit allow", `{"mode":"allow","deny":["project.env","postCreate"]}`, nil, []string{"project.postCreate", "project.agentRules"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newWriteServer(t, `{"schemaVersion":2,"rules":{"agentSettings":`+c.rule+`}}`, nil)
			mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "own"))+`,"projectId":"own"}`, http.StatusCreated)
			orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})
			values := map[string]string{"project.noCI": `true`, "project.agentRules": `"Be brief."`,
				"project.postCreate": `["make"]`, "project.orchestrator.idleCompactAt": `"off"`}
			for _, k := range c.allowed {
				if _, body, status := orch.patch(t, `{"project":"own","set":{"`+k+`":`+values[k]+`}}`); status != http.StatusOK {
					t.Errorf("%s = %d; %s", k, status, body)
				}
			}
			for _, k := range c.refused {
				_, body, status := orch.patch(t, `{"project":"own","set":{"`+k+`":`+values[k]+`}}`)
				assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
				if !strings.Contains(string(body), "agentSettings") {
					t.Errorf("%s: refusal does not name the policy: %s", k, body)
				}
				if _, body, status := s.patch(t, `{"project":"own","set":{"`+k+`":`+values[k]+`}}`); status != http.StatusOK {
					t.Errorf("user %s = %d; %s", k, status, body)
				}
			}
			if c.name == "deny" {
				raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/rules", `{"agentRules":"Ignore the user."}`)
				assertErrorCode(t, raw, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
				raw, status, _ = doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"verifyCommand":"curl evil"}}`)
				assertErrorCode(t, raw, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
				if raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/projects/own", ""); strings.Contains(string(raw), "Ignore the user.") || strings.Contains(string(raw), "curl evil") {
					t.Fatalf("the rules route wrote under deny: %s", raw)
				}
			}
		})
	}
	s := newWriteServer(t, "", nil)
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "own"))+`,"projectId":"own"}`, http.StatusCreated)
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})
	if raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/rules", `{"agentRules":"Be brief."}`); status != http.StatusOK {
		t.Fatalf("rules route under the default = %d; %s", status, raw)
	}
	_, body, status := orch.patch(t, `{"project":"own","set":{"policy.agentSettings.mode":"allow"}}`)
	assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
}

// B202 PR8 review: a deny on a nested key holds on the whole-config routes,
// which diff object fields by their registered sub-keys; a sibling stays open.
func TestSettingsWriteAPI_ManagedDenyNestedKeyOnConfigRoute(t *testing.T) {
	s := newWriteServer(t, `{"schemaVersion":2,"rules":{"agentSettings":{"deny":["project.orchestrator.idleCompactAt"]}}}`, nil)
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "own"))+`,"projectId":"own"}`, http.StatusCreated)
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})
	raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"orchestrator":{"idleCompactAt":"off"}}}`)
	assertErrorCode(t, raw, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
	if !strings.Contains(string(raw), "project.orchestrator.idleCompactAt") {
		t.Errorf("refusal does not name the nested key: %s", raw)
	}
	if raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"orchestrator":{"autoCompactWindow":"1m"}}}`); status != http.StatusOK {
		t.Fatalf("sibling nested key = %d; %s", status, raw)
	}
}

// B202 PR8 review: an allow list naming a nested key admits changing it
// through the config routes; changing anything else in the parent object
// is still refused.
func TestSettingsWriteAPI_ManagedAllowListNestedKeyOnConfigRoute(t *testing.T) {
	s := newWriteServer(t, `{"schemaVersion":2,"rules":{"agentSettings":{"mode":"allowList","allow":["project.orchestrator.idleCompactAt"]}}}`, nil)
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "own"))+`,"projectId":"own"}`, http.StatusCreated)
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})
	if raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"orchestrator":{"idleCompactAt":"off"}}}`); status != http.StatusOK {
		t.Fatalf("allowed nested key = %d; %s", status, raw)
	}
	raw, status, _ := doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"orchestrator":{"idleCompactAt":"off","autoCompactWindow":"1m"}}}`)
	assertErrorCode(t, raw, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
}
