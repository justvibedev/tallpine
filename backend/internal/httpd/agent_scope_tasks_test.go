package httpd

import (
	"testing"
)

const stopTaskPath = "/background-tasks/stop"

func stopOf(session string) string { return "/api/v1/sessions/" + session + stopTaskPath }

// Stopping a background task is for the agent's own tasks; an orchestrator may
// also stop the tasks of workers in its own project. Nothing else.
func TestAgentTokensStopOnlyTheirOwnOrOwnProjectWorkerTasks(t *testing.T) {
	h, store, tokens := approvalsHarness(t, nil)

	// A worker: only itself.
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"POST", stopOf("app-1"), 200},
		{"POST", stopOf("app-2"), 403},    // a peer worker
		{"POST", stopOf("app-orch"), 403}, // its orchestrator
		{"POST", stopOf("web-1"), 403},    // another project
		{"POST", stopOf("nope"), 403},     // unknown session
	})
	// An orchestrator: itself, and workers of its own project only.
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"POST", stopOf("app-orch"), 200},
		{"POST", stopOf("app-1"), 200},
		{"POST", stopOf("app-2"), 200},
		{"POST", stopOf("app-orch2"), 403}, // another orchestrator
		{"POST", stopOf("web-1"), 403},     // another project's worker
		{"POST", stopOf("nope"), 403},
	})
}

func TestReviewerTokensNeverStopTasks(t *testing.T) {
	h, _, tokens := approvalsHarness(t, nil)
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"POST", stopOf("app-1"), 403},
	})
}
