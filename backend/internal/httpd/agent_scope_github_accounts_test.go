package httpd

import "testing"

// An orchestrator lists the signed-in gh accounts for its own project only;
// workers and reviewers get neither route.
func TestAgentScopeGitHubAccounts(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)

	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"GET", "/api/v1/github/accounts", 200},
		{"GET", "/api/v1/projects/app/github-accounts", 200},
		{"GET", "/api/v1/projects/web/github-accounts", 403},
		{"PATCH", "/api/v1/settings/values", 200},
	})
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"GET", "/api/v1/github/accounts", 403},
		{"GET", "/api/v1/projects/app/github-accounts", 403},
	})
}
