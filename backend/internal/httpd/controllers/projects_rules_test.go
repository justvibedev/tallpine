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
