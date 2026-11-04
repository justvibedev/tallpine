package httpd

import "testing"

func TestAgentScopeBacklogRoles(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)

	// Workers file into and read their own project's backlog, and nothing else.
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{
		{"GET", "/api/v1/projects/app/backlog", 200},
		{"POST", "/api/v1/projects/app/backlog", 200},
		{"GET", "/api/v1/projects/app/backlog/next", 200},
		{"GET", "/api/v1/projects/app/backlog/B1", 200},
		{"PATCH", "/api/v1/projects/app/backlog/B1", 403},
		{"POST", "/api/v1/projects/app/backlog/B1/state", 403},
		{"POST", "/api/v1/projects/app/backlog/reorder", 403},
		{"POST", "/api/v1/projects/app/backlog/B1/links", 403},
		{"DELETE", "/api/v1/projects/app/backlog/B1/links", 403},
		{"PUT", "/api/v1/projects/app/backlog/B1/fields/area", 403},
		{"DELETE", "/api/v1/projects/app/backlog/B1/fields/area", 403},
		{"DELETE", "/api/v1/projects/app/backlog/B1", 403},
		{"POST", "/api/v1/projects/app/backlog/purge", 403},
		{"GET", "/api/v1/projects/web/backlog", 403},
		{"POST", "/api/v1/projects/web/backlog", 403},
	})
	// The orchestrator manages its own project's backlog only.
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{
		{"POST", "/api/v1/projects/app/backlog", 200},
		{"PATCH", "/api/v1/projects/app/backlog/B1", 200},
		{"POST", "/api/v1/projects/app/backlog/B1/state", 200},
		{"POST", "/api/v1/projects/app/backlog/reorder", 200},
		{"POST", "/api/v1/projects/app/backlog/B1/links", 200},
		{"DELETE", "/api/v1/projects/app/backlog/B1/links", 200},
		{"PUT", "/api/v1/projects/app/backlog/B1/fields/area", 200},
		{"DELETE", "/api/v1/projects/app/backlog/B1/fields/area", 200},
		{"DELETE", "/api/v1/projects/app/backlog/B1", 200},
		{"POST", "/api/v1/projects/app/backlog/purge", 200},
		{"GET", "/api/v1/projects/web/backlog", 403},
		{"PATCH", "/api/v1/projects/web/backlog/B1", 403},
		{"DELETE", "/api/v1/projects/web/backlog/B1", 403},
		{"POST", "/api/v1/projects/web/backlog/purge", 403},
	})
	// Reviewers get nothing.
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{
		{"GET", "/api/v1/projects/app/backlog", 403},
		{"POST", "/api/v1/projects/app/backlog", 403},
		{"DELETE", "/api/v1/projects/app/backlog/B1", 403},
		{"POST", "/api/v1/projects/app/backlog/purge", 403},
	})
}

func TestAgentScopeBacklogInboxCounts(t *testing.T) {
	store, tokens := newAgentScopeFixture()
	h := newAgentScopeHarness(t, store, tokens)
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{{"GET", "/api/v1/backlog/inbox-counts", 200}})
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{{"GET", "/api/v1/backlog/inbox-counts", 403}})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{{"GET", "/api/v1/backlog/inbox-counts", 403}})
	// The daemon-side release install (B433) is for orchestrators only.
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{{"POST", "/api/v1/release/install", 200}})
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{{"POST", "/api/v1/release/install", 403}})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{{"POST", "/api/v1/release/install", 403}})
	// request-build (B461) is open to workers; install stays orchestrator-only.
	h.check(store.tokenFor(tokens, "app-1"), []scopeCase{{"POST", "/api/v1/release/request-build", 200}, {"POST", "/api/v1/release/install", 403}})
	h.check(store.tokenFor(tokens, "app-orch"), []scopeCase{{"POST", "/api/v1/release/request-build", 200}})
	h.check(tokens.IssueReviewer("rev-app-1", "app-1"), []scopeCase{{"POST", "/api/v1/release/request-build", 403}})
}
