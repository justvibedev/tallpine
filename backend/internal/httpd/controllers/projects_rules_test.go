package controllers_test

import (
	"net/http"
	"strings"
	"testing"
)

// B189: the rules route takes only agentRules/orchestratorRules; any other
// config field in the body is refused, and stored fields survive.
func TestProjectsAPI_SetRulesOnlyRules(t *testing.T) {
	srv := newTestServer(t)
	repo := gitRepo(t, "rules")
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"rules","config":{"postCreate":["make setup"]}}`)
	if status != http.StatusCreated {
		t.Fatalf("seed = %d; %s", status, body)
	}
	for _, bad := range []string{
		`{"agentRules":"x","env":{"K":"V"}}`,
		`{"orchestratorRules":"x","postCreate":["evil"]}`,
		`{"config":{"agentRules":"x"}}`,
		`{"agentRules":"x","agentConfig":{"permissions":"bypass-permissions"}}`,
		`{"agentRules":"x","Actor":"user"}`,
	} {
		body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/rules/rules", bad)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	}
	// A body past the 64 KiB limit is refused before it is read in full.
	huge := `{"orchestratorRules":"` + strings.Repeat("z", 70<<10) + `"}`
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/rules/rules", huge)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	over := `{"orchestratorRules":"` + strings.Repeat("z", 9<<10) + `"}`
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/rules/rules", over)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_PROJECT_CONFIG")
	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/projects/rules/rules", `{"orchestratorRules":"Delegate."}`)
	if status != http.StatusOK {
		t.Fatalf("set rules = %d; %s", status, body)
	}
	if s := string(body); !strings.Contains(s, `"orchestratorRules":"Delegate."`) || !strings.Contains(s, `"make setup"`) {
		t.Fatalf("response lost a field: %s", s)
	}
}

// B643: the role-settings route takes only agent/model/effort per role; any
// other config field is a 400 and stored fields survive.
func TestProjectsAPI_SetRoleSettings(t *testing.T) {
	srv := newTestServer(t)
	repo := gitRepo(t, "roles")
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":`+quote(repo)+`,"projectId":"roles","config":{"postCreate":["make setup"],"env":{"K":"V"}}}`)
	if status != http.StatusCreated {
		t.Fatalf("seed = %d; %s", status, body)
	}
	for _, bad := range []string{
		`{"worker":{"effort":"low"},"env":{"K":"V"}}`,
		`{"worker":{"effort":"low","permissions":"bypass-permissions"}}`,
		`{"config":{"postCreate":["evil"]}}`,
		`{"planner":{"agent":"codex"}}`,
	} {
		body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/projects/roles/role-settings", bad)
		assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	}
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/projects/roles/role-settings", `{"worker":{"effort":"turbo"}}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_ROLE_SETTINGS")
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/projects/roles/role-settings", `{"reviewer":{"agent":"nope"}}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_ROLE_SETTINGS")
	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/projects/roles/role-settings", `{"worker":{"agent":"codex","model":"m","effort":"high"},"reviewer":{"agent":"claude-code","effort":"low"}}`)
	if status != http.StatusOK {
		t.Fatalf("set = %d; %s", status, body)
	}
	s := string(body)
	for _, want := range []string{`"make setup"`, `"K":"V"`, `"effort":"high"`, `"reviewers"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("response lacks %s: %s", want, s)
		}
	}
}
