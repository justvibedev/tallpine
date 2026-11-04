package httpd

import "testing"

// Intents are the orchestrator's: workers and reviewers get nothing, and the
// orchestrator stays inside its own project.
func TestAgentScopeIntentsRoles(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	base := "/api/v1/projects/app/intents"
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"GET", base, 403},
		{"POST", base, 403},
		{"POST", base + "/I1/done", 403},
	})
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"GET", base, 200},
		{"POST", base, 200},
		{"POST", base + "/I1/done", 200},
		{"GET", "/api/v1/projects/web/intents", 403},
		{"POST", "/api/v1/projects/web/intents", 403},
		{"POST", "/api/v1/projects/web/intents/I1/done", 403},
	})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"GET", base, 403},
		{"POST", base, 403},
		{"POST", base + "/I1/done", 403},
	})
}
