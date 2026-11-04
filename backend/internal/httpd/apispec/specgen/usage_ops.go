package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

type renderSourceQuery struct {
	Source *string `query:"source,omitempty" enum:"1" description:"Set to 1 to read the page as the agent wrote it: the stored bytes as text/plain, without the theme bootstrap."`
}

type conversationSnapshotQuery struct {
	BeforeSequence *int64 `query:"beforeSequence,omitempty" minimum:"1" description:"Read items older than this conversation sequence. Omit for the newest page."`
	Limit          *int64 `query:"limit,omitempty" minimum:"1" maximum:"500" description:"Maximum combined messages and activities to return. Defaults to 200."`
}

func usageOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/usage/sessions", id: "listCompactSessionUsage", tag: "usage",
			summary:    "List compact token and estimated cost usage for session cards",
			pathParams: []any{controllers.ListUsageSessionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListCompactSessionUsageResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/usage/sessions/memory", id: "listSessionMemory", tag: "usage",
			summary:    "List resident memory of each live session's runtime process tree",
			pathParams: []any{controllers.ListUsageSessionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionMemoryResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/usage/memory/pressure", id: "getMemoryPressure", tag: "usage",
			summary: "Read the machine's memory-pressure verdict without sampling any process",
			resps: []respUnit{
				{http.StatusOK, controllers.MemoryPressureResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/usage/sessions/{sessionId}", id: "getSessionUsage", tag: "usage",
			summary:    "Get detailed token and estimated cost usage for one session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionUsageResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
