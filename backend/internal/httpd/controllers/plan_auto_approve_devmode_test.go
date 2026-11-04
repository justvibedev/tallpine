package controllers_test

import (
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// Dev mode lifts the agent limits, not the human-only keys: an orchestrator in
// Dev mode still cannot write planAutoApprove, on this project or another one,
// by key or through the project config routes.
func TestDevModeOrchestratorStillCannotWriteHumanOnlyKeys(t *testing.T) {
	s := newWriteServer(t, "", nil)
	for _, id := range []string{"own", "other"} {
		mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(gitRepo(t, id))+`,"projectId":"`+id+`"}`, http.StatusCreated)
	}
	as := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own", DevMode: true})
	for _, project := range []string{"own", "other"} {
		_, raw, status := as.patch(t, `{"project":"`+project+`","set":{"project.planAutoApprove":true}}`)
		if status != http.StatusForbidden || !strings.Contains(string(raw), "NOT_PERMITTED") {
			t.Errorf("PATCH %s = %d; %s", project, status, raw)
		}
		for _, path := range []string{"/api/v1/projects/" + project + "/config", "/api/v1/projects/" + project} {
			raw, status, _ := doRequest(t, as.srv, "PUT", path, `{"config":{"planAutoApprove":true}}`)
			if status != http.StatusForbidden {
				t.Errorf("PUT %s = %d; %s", path, status, raw)
			}
		}
	}
	// Dev mode does open another project's ordinary settings.
	if _, raw, status := as.patch(t, `{"project":"other","set":{"project.noCI":true}}`); status != http.StatusOK {
		t.Errorf("dev-mode write to another project = %d; %s", status, raw)
	}
}
