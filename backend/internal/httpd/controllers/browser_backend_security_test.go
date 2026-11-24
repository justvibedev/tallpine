package controllers_test

import (
	"context"
	"net/http"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// browserBackend=chrome hands agents the person's real Chrome logins (B849
// a1), so no agent token writes it on any route; a person can, and only to
// electron or chrome.
func TestBrowserBackend_OnlyAPersonWritesIt(t *testing.T) {
	s := newWriteServer(t, "", nil)
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "own"))+`,"projectId":"own"}`, http.StatusCreated)
	backend := func() string {
		t.Helper()
		rec, ok, err := s.store.GetProject(context.Background(), "own")
		if err != nil || !ok {
			t.Fatalf("GetProject = %v, %v", ok, err)
		}
		if rec.Config.BrowserBackend == nil {
			return ""
		}
		return *rec.Config.BrowserBackend
	}
	for name, agent := range map[string]requestscope.Agent{
		"orchestrator": {Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", CrossProject: true},
		"worker":       {Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"},
	} {
		as := s.as(t, agent)
		if _, raw, status := as.patch(t, `{"project":"own","set":{"project.browserBackend":"chrome"}}`); status != http.StatusForbidden {
			t.Errorf("%s PATCH = %d; %s", name, status, raw)
		}
		for _, path := range []string{"/api/v1/projects/own/config", "/api/v1/projects/own"} {
			if raw, status, _ := doRequest(t, as.srv, "PUT", path, `{"config":{"browserBackend":"chrome"}}`); status != http.StatusForbidden {
				t.Errorf("%s PUT %s = %d; %s", name, path, status, raw)
			}
		}
	}
	if got := backend(); got != "" {
		t.Fatalf("an agent write set browserBackend = %q", got)
	}

	if _, raw, status := s.patch(t, `{"project":"own","set":{"project.browserBackend":"firefox"}}`); status != http.StatusBadRequest {
		t.Errorf("user PATCH firefox = %d; %s", status, raw)
	}
	if got, raw, status := s.patch(t, `{"project":"own","set":{"project.browserBackend":"chrome"}}`); status != http.StatusOK || got["project.browserBackend"].Value != "chrome" {
		t.Fatalf("user PATCH chrome = %d; %s", status, raw)
	}
	if got := backend(); got != "chrome" {
		t.Fatalf("browserBackend = %q, want chrome", got)
	}
	mustStatus(t, s.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"browserBackend":"electron"}}`, http.StatusOK)
	if got := backend(); got != "electron" {
		t.Fatalf("browserBackend = %q, want electron", got)
	}
}
