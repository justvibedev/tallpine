package controllers_test

import (
	"net/http"
	"strings"
	"testing"
)

// B847: the drift and apply routes list a project whose orchestrator differs
// from the app default and, on apply, bring it in line.
func TestRoleDefaultDriftAndApplyRoutes(t *testing.T) {
	srv := newTestServer(t)
	repo := gitRepo(t, "towers")
	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"towers"}`); status != http.StatusCreated {
		t.Fatalf("seed = %d; %s", status, body)
	}
	// The store's built-in app default is claude-code / claude-opus-5-5 / medium.
	if body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/projects/towers/role-settings", `{"orchestrator":{"agent":"claude-code","model":"claude-opus-5-5","effort":"high"}}`); status != http.StatusOK {
		t.Fatalf("role settings = %d; %s", status, body)
	}
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/role-defaults/orchestrator/drift", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"projectId":"towers"`) || !strings.Contains(string(body), `"effort":"high"`) {
		t.Fatalf("drift = %d; %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/settings/role-defaults/orchestrator/apply", `{"projectIds":["towers"]}`)
	if status != http.StatusOK || !strings.Contains(string(body), `"applied":["towers"]`) {
		t.Fatalf("apply = %d; %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/role-defaults/orchestrator/drift", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"projects":[]`) {
		t.Fatalf("drift after apply = %d; %s", status, body)
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/settings/role-defaults/planner/drift", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_ROLE")
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/settings/role-defaults/orchestrator/apply", `{"bogus":1}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

// An agent token may not read the comparison or rewrite projects through it.
func TestRoleDefaultRoutesRefuseAgents(t *testing.T) {
	f := newScopeFixture(t)
	orch := f.as("app-orch")
	for _, req := range [][2]string{{"GET", "/api/v1/settings/role-defaults/orchestrator/drift"}, {"POST", "/api/v1/settings/role-defaults/orchestrator/apply"}} {
		if status, body := f.do(orch, req[0], req[1], ""); status != http.StatusForbidden || !strings.Contains(body, "AGENT_SCOPE_FORBIDDEN") {
			t.Errorf("%s %s = %d %s, want 403", req[0], req[1], status, body)
		}
	}
}
