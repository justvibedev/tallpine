package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/policytest"
	"tallpine.eu/tallpine/backend/internal/agentpolicy/rules"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	projectsvc "tallpine.eu/tallpine/backend/internal/service/project"
	settingssvc "tallpine.eu/tallpine/backend/internal/service/settings"
	"tallpine.eu/tallpine/backend/internal/service/settingsaudit"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

type auditCleanup struct{ policy cleanupsvc.Policy }

func (f *auditCleanup) Status(context.Context) (cleanupsvc.Status, error) {
	return cleanupsvc.Status{Policy: f.policy}, nil
}

func (f *auditCleanup) UpdatePolicy(_ context.Context, patch []byte) (cleanupsvc.Policy, error) {
	p, err := cleanupsvc.ApplyPatch(f.policy, patch)
	if err == nil {
		f.policy = p
	}
	return p, err
}

func (f *auditCleanup) Run(context.Context, cleanupsvc.RunRequest) (cleanupsvc.Report, error) {
	return cleanupsvc.Report{}, nil
}

func (f *auditCleanup) History(context.Context, int, bool) ([]cleanupsvc.Report, error) {
	return nil, nil
}

// memSettings is an in-memory settings.Store seeded with fresh-install values.
type memSettings struct{ s settingssvc.Snapshot }

func newMemSettings() *memSettings {
	return &memSettings{s: settingssvc.Snapshot{
		DefaultSessionMode: domain.SessionModeChat, ChatHibernation: domain.DefaultChatHibernationPolicy(),
		VersionRefresh: true, StorageCompaction: true, ProviderEventRetentionDays: settingssvc.DefaultProviderEventRetentionDays,
	}}
}

func (m *memSettings) GetAppSettings(context.Context) (settingssvc.Snapshot, error) { return m.s, nil }

func (m *memSettings) SetDefaultSessionMode(_ context.Context, mode domain.SessionMode, _ time.Time) error {
	m.s.DefaultSessionMode = mode
	return nil
}

func (m *memSettings) SetHarnessDefault(_ context.Context, a domain.AgentHarness, v domain.HarnessDefault, _ time.Time) error {
	m.s.HarnessDefaults = map[string]domain.HarnessDefault{string(a): v}
	return nil
}

func (m *memSettings) SetVersionRefresh(_ context.Context, on bool, _ time.Time) error {
	m.s.VersionRefresh = on
	return nil
}

func (m *memSettings) SetStorageCompaction(_ context.Context, on bool, days int, _ time.Time) error {
	m.s.StorageCompaction, m.s.ProviderEventRetentionDays = on, days
	return nil
}

func (m *memSettings) SetChatHibernation(_ context.Context, p domain.ChatHibernationPolicy, _ time.Time) error {
	m.s.ChatHibernation = p
	return nil
}

func newAuditServer(t *testing.T) (*httptest.Server, *sqlite.Store) {
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
	audit := settingsaudit.New(store, log)
	projects := projectsvc.New(store)
	projects.SetSettingsAudit(audit)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Projects:      projects,
		Settings:      settingssvc.New(newMemSettings(), nil, settingssvc.Offering{}, nil),
		AgentPolicy:   engine,
		Cleanup:       &auditCleanup{policy: cleanupsvc.DefaultPolicy()},
		SettingsAudit: audit,
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv, store
}

// changes returns the logged changes of project as key -> [old, new].
func changes(t *testing.T, store *sqlite.Store, project domain.ProjectID) map[string][2]string {
	t.Helper()
	rows, err := store.ListSettingsChanges(context.Background(), project, 100)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][2]string{}
	for _, r := range rows {
		if r.Actor != "user" {
			t.Errorf("%s actor = %q, want user", r.Key, r.Actor)
		}
		if _, dup := out[r.Key]; !dup {
			out[r.Key] = [2]string{r.OldValue, r.NewValue}
		}
	}
	return out
}

func mustStatus(t *testing.T, srv *httptest.Server, method, path, body string, want int) {
	t.Helper()
	got, status, _ := doRequest(t, srv, method, path, body)
	if status != want {
		t.Fatalf("%s %s = %d; %s", method, path, status, got)
	}
}

// B202: every project config write route logs the keys it changed, with the
// actor, and redacts env values.
func TestSettingsAudit_ProjectRoutes(t *testing.T) {
	srv, store := newAuditServer(t)
	repo := gitRepo(t, "audited")
	mustStatus(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"audited"}`, http.StatusCreated)

	mustStatus(t, srv, "PUT", "/api/v1/projects/audited/config", `{"config":{"noCI":true,"env":{"TOKEN":"secret"}}}`, http.StatusOK)
	got := changes(t, store, "audited")
	if got["project.noCI"] != [2]string{"null", "true"} {
		t.Errorf("config route noCI = %v", got["project.noCI"])
	}
	if got["project.env"] != [2]string{settingsaudit.Redacted, settingsaudit.Redacted} {
		t.Errorf("env not redacted: %v", got["project.env"])
	}

	mustStatus(t, srv, "PUT", "/api/v1/projects/audited/rules", `{"agentRules":"Be brief."}`, http.StatusOK)
	if got := changes(t, store, "audited")["project.agentRules"]; got != [2]string{"null", `"Be brief."`} {
		t.Errorf("rules route = %v", got)
	}

	mustStatus(t, srv, "PUT", "/api/v1/projects/audited", `{"displayName":"Audited","config":{"noCI":true,"env":{"TOKEN":"secret"},"agentRules":"Be brief.","defaultBranch":"dev"}}`, http.StatusOK)
	if got := changes(t, store, "audited")["project.defaultBranch"]; got != [2]string{"null", `"dev"`} {
		t.Errorf("settings route = %v", got)
	}

	mustStatus(t, srv, "PATCH", "/api/v1/projects/audited/permissions", `{"permissions":"auto"}`, http.StatusOK)
	if got := changes(t, store, "audited")["project.agentConfig"]; got[1] == "" || got[0] == got[1] {
		t.Errorf("permissions route = %v", got)
	}

	rows, _ := store.ListSettingsChanges(context.Background(), "audited", 100)
	before := len(rows)
	mustStatus(t, srv, "PUT", "/api/v1/projects/audited/rules", `{"agentRules":"Be brief."}`, http.StatusOK)
	if rows, _ = store.ListSettingsChanges(context.Background(), "audited", 100); len(rows) != before {
		t.Errorf("an unchanged write logged %d rows", len(rows)-before)
	}
}

// B202: app settings, agent policy and cleanup policy writes log their keys.
func TestSettingsAudit_AppRoutes(t *testing.T) {
	srv, store := newAuditServer(t)
	mustStatus(t, srv, "PATCH", "/api/v1/settings/version-refresh", `{"enabled":false}`, http.StatusOK)
	mustStatus(t, srv, "PATCH", "/api/v1/settings/chat-hibernation", `{"workerIdleSeconds":600}`, http.StatusOK)
	mustStatus(t, srv, "PATCH", "/api/v1/settings/storage-compaction", `{"providerEventRetentionDays":30}`, http.StatusOK)
	mustStatus(t, srv, "PATCH", "/api/v1/settings/session-interface", `{"defaultSessionMode":"tui"}`, http.StatusOK)
	mustStatus(t, srv, "PATCH", "/api/v1/settings/agent-policy", `{"rules":{"permissionCap":{"max":"auto"}}}`, http.StatusOK)
	mustStatus(t, srv, "PATCH", "/api/v1/cleanup/policy", `{"automatic":false}`, http.StatusOK)
	got := changes(t, store, "")
	want := map[string][2]string{
		"app.versionRefresh":                    {"true", "false"},
		"app.chatHibernation.workerIdleSeconds": {"1200", "600"},
		"app.providerEventRetentionDays":        {"14", "30"},
		"app.defaultSessionMode":                {`"chat"`, `"tui"`},
		"policy.permissionCap.max":              {"null", `"auto"`},
		"app.cleanup.automatic":                 {"true", "false"},
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("logged keys = %v", got)
	}
}
