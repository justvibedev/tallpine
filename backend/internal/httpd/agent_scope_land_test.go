package httpd

import "testing"

func landOf(session string) string { return "/api/v1/sessions/" + session + "/land" }

// Landing a branch moves the target branch: never a worker (not even its own
// session) or a reviewer; an orchestrator only for its own project's sessions.
func TestOnlyOrchestratorTokensLandSessionBranches(t *testing.T) {
	h, store, tokens := approvalsHarness(t, nil)
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"POST", landOf("app-1"), 403}, // its own session
		{"POST", landOf("app-2"), 403},
	})
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"POST", landOf("app-1"), 200},
		{"POST", landOf("web-1"), 403}, // another project's worker
	})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"POST", landOf("app-1"), 403},
	})
}
