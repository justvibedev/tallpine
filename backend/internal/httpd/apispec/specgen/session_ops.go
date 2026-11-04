package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func sessionOperations() []operation {
	ops := append([]operation{}, sessionOperationsPart1()...)
	ops = append(ops, sessionOperationsPart2()...)
	ops = append(ops, sessionOperationsPart3()...)
	return ops
}

func sessionOperationsPart1() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/sessions", id: "listSessions", tag: "sessions",
			summary:    "List sessions",
			pathParams: []any{controllers.ListSessionsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions", id: "spawnSession", tag: "sessions",
			summary: "Spawn a new agent session",
			reqBody: controllers.SpawnSessionRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.SpawnSessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}", id: "getSession", tag: "sessions",
			summary:    "Fetch one session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/pin", id: "pinSession", tag: "sessions",
			summary:    "Pin a session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/pin", id: "unpinSession", tag: "sessions",
			summary:    "Unpin a session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview", id: "getSessionPreview", tag: "sessions",
			summary:    "Discover a browser preview URL for a session workspace",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionPreviewResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/preview", id: "setSessionPreview", tag: "sessions",
			summary:    "Set (or autodetect) the browser preview URL for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetSessionPreviewRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/preview", id: "clearSessionPreview", tag: "sessions",
			summary:    "Clear the browser preview URL for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview/server", id: "getSessionPreviewServer", tag: "sessions",
			summary:    "Get the managed preview server status for a session",
			pathParams: []any{controllers.SessionIDParam{}, controllers.BrowserCapabilityHeader{}, controllers.PreviewCapabilityHeader{}},
			resps: []respUnit{
				{http.StatusOK, controllers.PreviewServerStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/preview/server", id: "startSessionPreviewServer", tag: "sessions",
			summary:    "Start a session-owned server from .tallpine/launch.json and open its application preview",
			pathParams: []any{controllers.SessionIDParam{}, controllers.BrowserCapabilityHeader{}, controllers.PreviewCapabilityHeader{}},
			reqBody:    controllers.StartPreviewServerRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.PreviewServerStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusRequestTimeout, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusGatewayTimeout, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/preview/server", id: "stopSessionPreviewServer", tag: "sessions",
			summary:    "Stop the managed preview server for a session",
			pathParams: []any{controllers.SessionIDParam{}, controllers.BrowserCapabilityHeader{}, controllers.PreviewCapabilityHeader{}},
			resps: []respUnit{
				{http.StatusOK, controllers.PreviewServerStatusResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview/files/*", id: "getSessionPreviewFile", tag: "sessions",
			summary:    "Serve a static browser preview file from a session workspace or artifact directory",
			pathParams: []any{controllers.SessionIDParam{}, controllers.PreviewFileQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview/artifacts/*", id: "getSessionPreviewArtifact", tag: "sessions",
			summary:    "Serve a selected static browser preview file from the session artifact directory",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/artifact-files/*", id: "getSessionArtifactFile", tag: "sessions",
			summary:    "Serve a session artifact file sandboxed, an HTML page with the render theme bootstrap",
			pathParams: []any{controllers.SessionIDParam{}, renderSourceQuery{}},
			resps: []respUnit{
				{http.StatusOK, ""},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/artifact-files", id: "deleteSessionArtifactFile", tag: "sessions",
			summary:    "Delete one file from a session's artifact directory and return the session with its refreshed artifact list",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ArtifactFileQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/artifact-files", id: "updateSessionArtifactFile", tag: "sessions",
			summary:    "Replace the text content of one file in a session's artifact directory",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.UpdateArtifactFileRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ArtifactFileResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/artifact-versions", id: "listSessionArtifactVersions", tag: "sessions",
			summary:    "List the saved versions of one file in a session's artifact directory",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ArtifactVersionQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ListArtifactVersionsResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/artifact-versions/{version}", id: "getSessionArtifactVersion", tag: "sessions",
			summary:    "Read one saved version of an artifact file",
			pathParams: []any{controllers.SessionIDParam{}, controllers.ArtifactVersionParam{}, controllers.ArtifactVersionQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ArtifactVersionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/artifact-versions/sync", id: "syncSessionArtifactVersions", tag: "sessions",
			summary:    "Record an artifact file's unrecorded edit, then list its saved versions",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SyncArtifactVersionsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ListArtifactVersionsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/artifact-versions/restore", id: "restoreSessionArtifactVersion", tag: "sessions",
			summary:    "Restore an artifact file to an earlier saved version",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.RestoreArtifactVersionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ArtifactFileResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "getSessionPreviewApp", tag: "sessions",
			summary:      "Proxy the running session-owned application preview",
			pathParams:   []any{controllers.SessionIDParam{}},
			resps:        []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
			contentTypes: map[int]string{http.StatusOK: "text/html"},
		},
		{
			method: http.MethodHead, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "headSessionPreviewApp", tag: "sessions",
			summary:    "Read headers from the running session-owned application preview",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "postSessionPreviewApp", tag: "sessions",
			summary:    "Send a request to the running session-owned application preview",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "putSessionPreviewApp", tag: "sessions",
			summary:    "Send a request to the running session-owned application preview",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "patchSessionPreviewApp", tag: "sessions",
			summary:    "Send a request to the running session-owned application preview",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/preview/app/*", id: "deleteSessionPreviewApp", tag: "sessions",
			summary:    "Send a request to the running session-owned application preview",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/agent-browser/*", id: "getSessionAgentBrowser", tag: "sessions",
			summary:      "Proxy the host's agent browser stream viewer (page, stream socket, downloads)",
			pathParams:   []any{controllers.SessionIDParam{}},
			resps:        []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusBadGateway, ""}},
			contentTypes: map[int]string{http.StatusOK: "text/html", http.StatusBadGateway: "text/plain"},
		},
		{
			method: http.MethodHead, path: "/api/v1/sessions/{sessionId}/agent-browser/*", id: "headSessionAgentBrowser", tag: "sessions",
			summary:    "Read headers from the host's agent browser stream viewer",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusBadGateway, ""}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/agent-browser/*", id: "postSessionAgentBrowser", tag: "sessions",
			summary:      "Upload a file into the host's agent browser",
			pathParams:   []any{controllers.SessionIDParam{}},
			resps:        []respUnit{{http.StatusOK, ""}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusBadGateway, ""}},
			contentTypes: map[int]string{http.StatusBadGateway: "text/plain"},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/attachments", id: "stageSessionAttachments", tag: "sessions",
			summary:    "Write images into a running session's worktree and return their paths",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.StageSessionAttachmentsRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.StageSessionAttachmentsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/workspace/manifest", id: "getSessionWorkspaceManifest", tag: "sessions",
			summary:    "Get the compact changed-file manifest for the initial workspace review paint",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.WorkspaceManifestResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/workspace/history", id: "getSessionWorkspaceHistory", tag: "sessions",
			summary:    "Get lazy commit and upstream metadata for a workspace review",
			pathParams: []any{controllers.SessionIDParam{}, controllers.WorkspaceHistoryQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.WorkspaceHistoryResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
