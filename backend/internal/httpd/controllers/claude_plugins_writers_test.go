package controllers_test

import (
	"net/http"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
)

// claudePlugins.reviewer is the user's alone (B740 a6): the project's own
// orchestrator may switch worker and orchestrator plugins, never the
// reviewer's, on PATCH /settings/values (whole object or the nested key) and
// on the config route. The user's writes of every role round-trip.
func TestClaudePluginsReviewerIsHumanOnly(t *testing.T) {
	s := newWriteServer(t, "", nil)
	repo := gitRepo(t, "own")
	mustStatus(t, s.srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"own"}`, http.StatusCreated)
	orch := s.as(t, requestscope.Agent{Role: requestscope.RoleOrchestrator, SessionID: "own-orch", ProjectID: "own"})

	_, body, status := orch.patch(t, `{"project":"own","set":{"project.claudePlugins":{"worker":{"design@m":false}},"project.claudeAutoMemory":{"worker":false}}}`)
	if status != http.StatusOK {
		t.Fatalf("orchestrator worker plugins = %d; %s", status, body)
	}
	raw, _, _ := doRequest(t, s.srv, "GET", "/api/v1/projects/own", "")
	if !strings.Contains(string(raw), `"claudePlugins":{"worker":{"design@m":false}}`) {
		t.Fatalf("worker plugins not stored: %s", raw)
	}
	for name, req := range map[string]string{
		"object":     `{"project":"own","set":{"project.claudePlugins":{"worker":{"design@m":false},"reviewer":{"design@m":true}}}}`,
		"nested key": `{"project":"own","set":{"project.claudePlugins.reviewer":{"design@m":true}}}`,
	} {
		_, body, status := orch.patch(t, req)
		assertErrorCode(t, body, status, http.StatusForbidden, "AGENT_NOT_PERMITTED")
		if !strings.Contains(string(body), "project.claudePlugins.reviewer") {
			t.Errorf("%s: error does not name the reviewer key: %s", name, body)
		}
	}
	raw, status, _ = doRequest(t, orch.srv, "PUT", "/api/v1/projects/own/config", `{"config":{"claudePlugins":{"reviewer":{"design@m":true}}}}`)
	if status != http.StatusForbidden || !strings.Contains(string(raw), "project.claudePlugins.reviewer") {
		t.Fatalf("config route reviewer plugins = %d; %s", status, raw)
	}

	// The user sets every role, including the reviewer, and auto-memory.
	_, body, status = s.patch(t, `{"project":"own","set":{"project.claudePlugins":{"worker":{"design@m":false},"reviewer":{"design@m":true}},"project.claudeAutoMemory":{"orchestrator":true,"worker":false,"reviewer":false}}}`)
	if status != http.StatusOK {
		t.Fatalf("user plugins = %d; %s", status, body)
	}
	raw, _, _ = doRequest(t, s.srv, "GET", "/api/v1/projects/own", "")
	for _, want := range []string{`"claudePlugins":{"reviewer":{"design@m":true},"worker":{"design@m":false}}`, `"claudeAutoMemory":{"orchestrator":true,"reviewer":false,"worker":false}`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("project config lacks %s: %s", want, raw)
		}
	}
	// With the reviewer entry unchanged, the orchestrator may still edit the
	// worker's plugins in the same object.
	if _, body, status := orch.patch(t, `{"project":"own","set":{"project.claudePlugins":{"worker":{"design@m":true},"reviewer":{"design@m":true}}}}`); status != http.StatusOK {
		t.Fatalf("orchestrator worker edit keeping reviewer = %d; %s", status, body)
	}
}
