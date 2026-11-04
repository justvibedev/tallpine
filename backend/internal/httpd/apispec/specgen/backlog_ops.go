package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// backlogOperations declares the per-project backlog surface: deferred work
// items with a one-level hierarchy, typed links, custom fields and drag order.
func backlogOperations() []operation {
	project := []any{controllers.BacklogProjectIDParam{}}
	item := []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRefParam{}}
	field := []any{controllers.BacklogProjectIDParam{}, controllers.BacklogRefParam{}, controllers.BacklogFieldParam{}}
	errs := []respUnit{
		{http.StatusBadRequest, envelope.APIError{}},
		{http.StatusNotFound, envelope.APIError{}},
		{http.StatusConflict, envelope.APIError{}},
		{http.StatusInternalServerError, envelope.APIError{}},
		{http.StatusNotImplemented, envelope.APIError{}},
	}
	with := func(ok respUnit) []respUnit { return append([]respUnit{ok}, errs...) }
	return []operation{
		{method: http.MethodGet, path: "/api/v1/backlog/inbox-counts", id: "getBacklogInboxCounts", tag: "backlog",
			summary: "Inbox items waiting for triage, per project",
			resps:   with(respUnit{http.StatusOK, controllers.BacklogInboxCountsResponse{}})},
		{method: http.MethodPost, path: "/api/v1/release/request-build", id: "requestReleaseBuild", tag: "backlog",
			summary: "Ask the daemon to run release-local request-build for the caller's project (agents; a fenced shell cannot write release-builds)",
			resps:   with(respUnit{http.StatusAccepted, controllers.ReleaseBuildResponse{}})},
		{method: http.MethodPost, path: "/api/v1/release/install", id: "installRelease", tag: "backlog",
			summary: "Install a verified local release build (orchestrator only); the daemon runs the installer detached", reqBody: controllers.ReleaseInstallRequest{},
			resps: with(respUnit{http.StatusAccepted, controllers.ReleaseInstallResponse{}})},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/merges", id: "getMergeDigest", tag: "backlog",
			summary: "Recently merged and in-flight PRs by lane, with same-file overlaps", pathParams: []any{controllers.MergesProjectIDParam{}},
			resps: with(respUnit{http.StatusOK, controllers.MergeDigestResponse{}})},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/intents", id: "listOrchestratorIntents", tag: "backlog",
			summary: "List the orchestrator's durable intents (open only unless all=true)", pathParams: []any{controllers.IntentsProjectIDParam{}, controllers.IntentsQuery{}},
			resps: with(respUnit{http.StatusOK, controllers.ListIntentsResponse{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/intents", id: "createOrchestratorIntent", tag: "backlog",
			summary: "Record an owner intent or standing decision", pathParams: []any{controllers.IntentsProjectIDParam{}}, reqBody: controllers.CreateIntentRequest{},
			resps: with(respUnit{http.StatusCreated, controllers.IntentResponse{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/intents/{intentId}/done", id: "completeOrchestratorIntent", tag: "backlog",
			summary: "Clear an open intent", pathParams: []any{controllers.IntentIDParam{}},
			resps: with(respUnit{http.StatusNoContent, nil})},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/backlog", id: "listProjectBacklog", tag: "backlog",
			summary: "List a project's backlog items in drag order", pathParams: []any{controllers.BacklogProjectIDParam{}, controllers.BacklogListQuery{}},
			resps: with(respUnit{http.StatusOK, controllers.ListBacklogResponse{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/backlog", id: "createBacklogItem", tag: "backlog",
			summary: "File a backlog item (agents land in the inbox)", pathParams: project, reqBody: controllers.CreateBacklogRequest{},
			resps: with(respUnit{http.StatusCreated, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/backlog/next", id: "nextBacklogItem", tag: "backlog",
			summary:    "Topmost open, unheld, unblocked, non-parent item (null when none)",
			pathParams: []any{controllers.BacklogProjectIDParam{}, controllers.BacklogNextQuery{}},
			resps:      with(respUnit{http.StatusOK, controllers.NextBacklogResponse{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/backlog/reorder", id: "reorderBacklogItem", tag: "backlog",
			summary: "Move an item before or after another", pathParams: project, reqBody: controllers.BacklogReorderRequest{},
			resps: with(respUnit{http.StatusNoContent, nil})},
		{method: http.MethodGet, path: "/api/v1/projects/{projectId}/backlog/{ref}", id: "getBacklogItem", tag: "backlog",
			summary: "Get a backlog item with its links", pathParams: item,
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/backlog/purge", id: "purgeBacklog", tag: "backlog",
			summary: "Hard-delete closed (done or dropped) items, optionally only those closed long ago", pathParams: project, reqBody: controllers.BacklogPurgeRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogDeleteResponse{}})},
		{method: http.MethodDelete, path: "/api/v1/projects/{projectId}/backlog/{ref}", id: "deleteBacklogItem", tag: "backlog",
			summary: "Hard-delete a closed item and its closed children", pathParams: item,
			resps: with(respUnit{http.StatusOK, controllers.BacklogDeleteResponse{}})},
		{method: http.MethodPatch, path: "/api/v1/projects/{projectId}/backlog/{ref}", id: "updateBacklogItem", tag: "backlog",
			summary: "Edit a backlog item (partial)", pathParams: item, reqBody: controllers.UpdateBacklogRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/backlog/{ref}/state", id: "setBacklogItemState", tag: "backlog",
			summary: "Move an item between inbox, open, done and dropped", pathParams: item, reqBody: controllers.BacklogStateRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodPost, path: "/api/v1/projects/{projectId}/backlog/{ref}/links", id: "linkBacklogItem", tag: "backlog",
			summary: "Link an item to a session, PR, issue, artifact, memory or item (or a blocker)", pathParams: item, reqBody: controllers.BacklogLinkRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodDelete, path: "/api/v1/projects/{projectId}/backlog/{ref}/links", id: "unlinkBacklogItem", tag: "backlog",
			summary: "Remove a link from an item", pathParams: item, reqBody: controllers.BacklogLinkRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodPut, path: "/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}", id: "setBacklogItemField", tag: "backlog",
			summary: "Set a custom field (an empty value clears it)", pathParams: field, reqBody: controllers.BacklogFieldRequest{},
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
		{method: http.MethodDelete, path: "/api/v1/projects/{projectId}/backlog/{ref}/fields/{key}", id: "clearBacklogItemField", tag: "backlog",
			summary: "Clear a custom field", pathParams: field,
			resps: with(respUnit{http.StatusOK, controllers.BacklogItemEnvelope{}})},
	}
}
