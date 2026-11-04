package httpd

import "testing"

// Run mode is the orchestrator's: workers and reviewers get nothing, and the
// orchestrator stays inside its own project.
func TestAgentScopeBacklogRunRoles(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	runs := "/api/v1/projects/app/backlog-runs"
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"GET", runs, 403},
		{"POST", runs, 403},
		{"GET", runs + "/R1", 403},
		{"PUT", runs + "/R1/plan", 403},
		{"POST", runs + "/R1/stop", 403},
		{"POST", runs + "/R1/items/B1", 403},
		{"GET", "/api/v1/projects/app/backlog/B1/usage", 403},
	})
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"GET", runs, 200},
		{"POST", runs, 200},
		{"GET", runs + "/R1", 200},
		{"PUT", runs + "/R1/plan", 200},
		{"POST", runs + "/R1/pause", 200},
		{"POST", runs + "/R1/resume", 200},
		{"POST", runs + "/R1/stop", 200},
		{"POST", runs + "/R1/finish", 200},
		{"POST", runs + "/R1/items/B1", 200},
		{"GET", "/api/v1/projects/app/backlog/B1/usage", 200},
		{"GET", "/api/v1/projects/web/backlog-runs", 403},
		{"POST", "/api/v1/projects/web/backlog-runs", 403},
	})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"GET", runs, 403},
		{"POST", runs, 403},
	})
}
