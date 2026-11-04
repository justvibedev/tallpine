package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func sessionOperationsPart3() []operation {
	return []operation{
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/restore", id: "restoreSession", tag: "sessions",
			summary:    "Restore a terminated session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RestoreSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/exit-agent", id: "exitAgent", tag: "sessions",
			summary:    "Exit the agent while preserving its Tallpine session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ExitAgentResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/resume-agent", id: "resumeAgent", tag: "sessions",
			summary:    "Resume an exited agent in its existing session",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ResumeAgentResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/interface-transition", id: "getSessionInterfaceTransition", tag: "sessions",
			summary:    "Inspect TUI and Chat interface handoff support and progress",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionInterfaceTransitionStatusResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/interface-transition", id: "startSessionInterfaceTransition", tag: "sessions",
			summary:    "Switch a live session between its TUI and Chat controllers",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.StartSessionInterfaceTransitionRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.StartSessionInterfaceTransitionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/sessions/{sessionId}/interface-transition", id: "cancelSessionInterfaceTransition", tag: "sessions",
			summary:    "Cancel an interface handoff before its source controller stops",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.CancelSessionInterfaceTransitionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/sessions/{sessionId}/interface-transition/{transitionId}/notice-acknowledgement", id: "acknowledgeSessionInterfaceTransitionNotice", tag: "sessions",
			summary:    "Acknowledge a failed or recovered interface handoff notice",
			pathParams: []any{controllers.SessionIDParam{}, controllers.SessionInterfaceTransitionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.InterfaceTransitionNoticeAckResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/kill", id: "killSession", tag: "sessions",
			summary:         "Mark a session terminated and tear down runtime/workspace resources",
			pathParams:      []any{controllers.SessionIDParam{}},
			reqBody:         controllers.KillSessionRequest{},
			optionalReqBody: true,
			resps: []respUnit{
				{http.StatusOK, controllers.KillSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/rollback", id: "rollbackSession", tag: "sessions",
			summary:    "Undo a partially-completed spawn (delete seed row, or kill if spawn output exists)",
			pathParams: []any{controllers.SessionIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.RollbackSessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/send", id: "sendSessionMessage", tag: "sessions",
			summary:    "Send a message to a running session's agent",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SendSessionMessageRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SendSessionMessageResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				// Conflict: the session is terminated, or paused on a permission
				// decision (SESSION_AWAITING_DECISION) — the guarded send refuses
				// to paste into a pending dialog.
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/activity", id: "setSessionActivity", tag: "sessions",
			summary:    "Report an agent activity-state signal for a session",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetActivityRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SetActivityResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/reviews/{reviewSessionID}/activity", id: "setReviewActivity", tag: "reviews",
			summary:    "Report a reviewer-owned hook signal",
			pathParams: []any{controllers.ReviewSessionIDParam{}},
			reqBody:    controllers.SetReviewActivityRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SetReviewActivityResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/orchestrators", id: "listOrchestrators", tag: "sessions",
			summary: "List orchestrator sessions across projects",
			resps: []respUnit{
				{http.StatusOK, controllers.ListSessionsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators", id: "spawnOrchestrator", tag: "sessions",
			summary: "Spawn an orchestrator session",
			reqBody: controllers.SpawnOrchestratorRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SpawnOrchestratorResponse{}},
				{http.StatusCreated, controllers.SpawnOrchestratorResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators/delegate", id: "delegateTask", tag: "sessions",
			summary: "Start a worker task and refine its title in the background",
			reqBody: controllers.DelegateTaskRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.DelegateTaskResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/tasks/prepare", id: "prepareTask", tag: "projects",
			summary:    "Speculatively create the next task's worktree",
			pathParams: []any{controllers.ProjectIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.PrepareTaskResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/task-preparations/{token}", id: "cancelTaskPreparation", tag: "projects",
			summary:    "Cancel an unclaimed speculative task worktree",
			pathParams: []any{controllers.TaskPreparationTokenParam{}},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators/{id}/handoff", id: "handoffOrchestrator", tag: "sessions",
			summary:    "Replace an orchestrator with a fresh one seeded from a state note",
			pathParams: []any{controllers.OrchestratorIDParam{}},
			reqBody:    controllers.HandoffOrchestratorRequest{},
			resps: []respUnit{
				{http.StatusCreated, controllers.SpawnOrchestratorResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators/{id}/research", id: "setOrchestratorResearch", tag: "sessions",
			summary:    "Switch an orchestrator's research mode, which raises its compaction window while it examines a large codebase itself",
			pathParams: []any{controllers.OrchestratorIDParam{}},
			reqBody:    controllers.OrchestratorResearchRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.OrchestratorResearchResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/orchestrators/{id}/research", id: "getOrchestratorResearch", tag: "sessions",
			summary:    "Report an orchestrator's research mode",
			pathParams: []any{controllers.OrchestratorIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.OrchestratorResearchResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/orchestrators/end", id: "endLane", tag: "sessions",
			summary: "End a lane: remove its orchestrator and move its workers to the default orchestrator",
			reqBody: controllers.EndLaneRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.EndLaneResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/orchestrators/{id}", id: "getOrchestrator", tag: "sessions",
			summary:    "Fetch one orchestrator session",
			pathParams: []any{controllers.OrchestratorIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/land", id: "landSessionBranch", tag: "sessions",
			summary:    "Squash an approved session branch without a PR onto the target branch (orchestrators and the owner)",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SessionLandRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionLandResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
