package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// planAutoApprove makes plans approve themselves, so only a person may turn it
// on: no agent token writes it on any route, not even the project's own
// orchestrator. Each case below is one write path.
func TestPlanAutoApprove_NoAgentCanWriteIt(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, id := range []string{"own", "created"} {
		repo := gitRepo(t, id)
		mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"`+id+`"}`, http.StatusCreated)
	}
	agents := map[string]requestscope.Agent{
		"orchestrator": {Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", CrossProject: true},
		"worker":       {Role: requestscope.RoleWorker, SessionID: "own-1", ProjectID: "own"},
	}
	flag := func(project string) bool {
		t.Helper()
		rec, ok, err := s.store.GetProject(context.Background(), project)
		if err != nil || !ok {
			t.Fatalf("GetProject(%s) = %v, %v", project, ok, err)
		}
		return rec.Config.PlanAutoApprove != nil && *rec.Config.PlanAutoApprove
	}
	for name, agent := range agents {
		as := s.as(t, agent)
		// settings by key (PATCH /settings/values), set and unset.
		for _, body := range []string{
			`{"project":"own","set":{"project.planAutoApprove":true}}`,
			`{"project":"own","unset":["project.planAutoApprove"]}`,
			`{"project":"own","set":{"project.noCI":true,"project.planAutoApprove":true}}`,
		} {
			_, raw, status := as.patch(t, body)
			if status != http.StatusForbidden || !strings.Contains(string(raw), "AGENT_NOT_PERMITTED") && !strings.Contains(string(raw), "NOT_PERMITTED") {
				t.Errorf("%s PATCH %s = %d; %s", name, body, status, raw)
			}
		}
		// project set-config / --config-json: PUT /projects/{id}/config and PUT /projects/{id}.
		for _, path := range []string{"/api/v1/projects/own/config", "/api/v1/projects/own", "/api/v1/projects/created/config", "/api/v1/projects/created"} {
			raw, status, _ := doRequest(t, as.srv, "PUT", path, `{"config":{"planAutoApprove":true}}`)
			if status != http.StatusForbidden {
				t.Errorf("%s PUT %s = %d; %s", name, path, status, raw)
			}
		}
		// Registering a project with it on.
		raw, status, _ := doRequest(t, as.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, "fresh-"+name))+`,"projectId":"fresh-`+name+`","config":{"planAutoApprove":true}}`)
		if status != http.StatusForbidden {
			t.Errorf("%s POST /projects = %d; %s", name, status, raw)
		}
	}
	if flag("own") || flag("created") {
		t.Fatal("an agent write changed planAutoApprove")
	}
	if rows, _ := s.store.ListSettingsChanges(context.Background(), "own", 50); len(rows) != 0 {
		t.Fatalf("refused writes were logged as changes: %+v", rows)
	}

	// A person can, by key and by config, and that is recorded as theirs.
	got, body, status := s.patch(t, `{"project":"own","set":{"project.planAutoApprove":true}}`)
	if status != http.StatusOK || got["project.planAutoApprove"].Value != true {
		t.Fatalf("user PATCH = %d; %s", status, body)
	}
	mustStatus(t, s.srv, "PUT", "/api/v1/projects/created/config", `{"config":{"planAutoApprove":true}}`, http.StatusOK)
	if !flag("created") {
		t.Fatal("the user's config write did not turn it on")
	}
	// An agent cannot switch it back off either: the toggle is a person's.
	_, raw, status := s.as(t, agents["orchestrator"]).patch(t, `{"project":"own","set":{"project.planAutoApprove":false}}`)
	if status != http.StatusForbidden {
		t.Fatalf("agent turning it off = %d; %s", status, raw)
	}
}
