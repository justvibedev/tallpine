package httpd

import (
	"strings"
	"testing"
)

// B189: an orchestrator may set only its own project's rules through the
// narrow rules route; the general config PUT stays closed for its own project,
// and workers, reviewers and other projects get neither.
func TestAgentScopeProjectRules(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)

	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"PUT", "/api/v1/projects/app/rules", 200},
		{"PUT", "/api/v1/projects/web/rules", 403},
		{"PUT", "/api/v1/projects/app/config", 403},
		{"PATCH", "/api/v1/projects/app/role-settings", 403},
		{"PUT", "/api/v1/projects/app", 403},
		{"PATCH", "/api/v1/projects/app/permissions", 403},
	})
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"PUT", "/api/v1/projects/app/rules", 403},
		{"PUT", "/api/v1/projects/app/config", 403},
		{"PATCH", "/api/v1/projects/app/role-settings", 403},
	})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"PUT", "/api/v1/projects/app/rules", 403},
	})
}

// B325: the refusal of an own-project config PUT names the rules flags.
func TestAgentScopeOwnConfigRefusalNamesRulesFlags(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	for _, path := range []string{"/api/v1/projects/app/config", "/api/v1/projects/app"} {
		rec := h.do(store.tokenFor(tokens, "app-orch"), "PUT", path)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), "--orchestrator-rules") || !strings.Contains(rec.Body.String(), "agentRulesFile") {
			t.Fatalf("%s: got %d %s", path, rec.Code, rec.Body.String())
		}
	}
}
