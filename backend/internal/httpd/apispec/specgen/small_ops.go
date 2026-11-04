package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

func automationOperations() []operation {
	id := []any{controllers.AutomationIDParam{}}
	commonErrors := []respUnit{
		{http.StatusBadRequest, envelope.APIError{}},
		{http.StatusNotFound, envelope.APIError{}},
		{http.StatusInternalServerError, envelope.APIError{}},
		{http.StatusNotImplemented, envelope.APIError{}},
	}
	return []operation{
		{method: http.MethodGet, path: "/api/v1/automations", id: "listAutomations", tag: "automations", summary: "List recurring automations", pathParams: []any{controllers.ListAutomationsQuery{}}, resps: append([]respUnit{{http.StatusOK, controllers.ListAutomationsResponse{}}}, commonErrors...)},
		{method: http.MethodPost, path: "/api/v1/automations", id: "createAutomation", tag: "automations", summary: "Create a recurring automation", reqBody: controllers.CreateAutomationRequest{}, resps: append([]respUnit{{http.StatusCreated, controllers.AutomationEnvelope{}}}, commonErrors...)},
		{method: http.MethodGet, path: "/api/v1/automations/{automationId}", id: "getAutomation", tag: "automations", summary: "Get one recurring automation", pathParams: id, resps: append([]respUnit{{http.StatusOK, controllers.AutomationEnvelope{}}}, commonErrors...)},
		{method: http.MethodPatch, path: "/api/v1/automations/{automationId}", id: "updateAutomation", tag: "automations", summary: "Update a recurring automation", pathParams: id, reqBody: controllers.UpdateAutomationRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.AutomationEnvelope{}}}, commonErrors...)},
		{method: http.MethodDelete, path: "/api/v1/automations/{automationId}", id: "deleteAutomation", tag: "automations", summary: "Delete an automation and its run history", pathParams: id, resps: append([]respUnit{{http.StatusNoContent, nil}}, commonErrors...)},
		{method: http.MethodGet, path: "/api/v1/automations/{automationId}/runs", id: "listAutomationRuns", tag: "automations", summary: "List durable automation run history", pathParams: []any{controllers.AutomationIDParam{}, controllers.ListAutomationRunsQuery{}}, resps: append([]respUnit{{http.StatusOK, controllers.ListAutomationRunsResponse{}}}, commonErrors...)},
	}
}

// linkPreviewOperations declares the server-side link unfurl. Must stay 1:1
// with the routes LinkPreviewController.Register mounts (enforced by the
// parity test).
func linkPreviewOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/link-preview", id: "getLinkPreview", tag: "link-preview",
			summary:    "Fetch link-preview metadata (Open Graph) for an external URL",
			pathParams: []any{controllers.LinkPreviewQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.LinkPreviewResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusBadGateway, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// endpointsOperations declares the phone's endpoint refresh. Not under
// /api/v1/mobile, which lanControlBlock 404s on the only listener a phone can
// reach.
func endpointsOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/endpoints", id: "getEndpoints", tag: "identity",
			summary: "List the ways this daemon can currently be reached",
			resps: []respUnit{
				{http.StatusOK, controllers.EndpointsResponse{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

// identityOperations declares the unauthenticated host-identity probe. It is
// the one route the Connect Mobile LAN listener serves without the connection
// password, so the phone can confirm which machine answered before presenting
// a credential. See docs/adr/0003-unauthenticated-identity-probe.md.
func identityOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/identity", id: "getIdentity", tag: "identity",
			summary: "Identify the daemon so a client can confirm which machine answered",
			resps: []respUnit{
				{http.StatusOK, controllers.IdentityResponse{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

func reportOperations() []operation {
	return []operation{{
		method: http.MethodGet, path: "/api/v1/reports", id: "listReports", tag: "reports",
		summary:    "List persisted project reports without changing delivery state",
		pathParams: []any{controllers.ListReportsQuery{}},
		resps: []respUnit{
			{http.StatusOK, controllers.ListReportsResponse{}},
			{http.StatusBadRequest, envelope.APIError{}},
			{http.StatusInternalServerError, envelope.APIError{}},
			{http.StatusNotImplemented, envelope.APIError{}},
		},
	}, {
		method: http.MethodPost, path: "/api/v1/reports", id: "createReport", tag: "reports",
		summary: "Persist a worker report for later orchestrator delivery",
		reqBody: controllers.CreateReportRequest{},
		resps: []respUnit{
			{http.StatusCreated, controllers.CreateReportResponse{}},
			{http.StatusBadRequest, envelope.APIError{}},
			{http.StatusNotFound, envelope.APIError{}},
			{http.StatusInternalServerError, envelope.APIError{}},
			{http.StatusNotImplemented, envelope.APIError{}},
		},
	}}
}

// systemOperations declares the startup requirements gate the desktop loading
// screen polls before showing the board, plus the real-install operations for
// the fixed system/install target allowlist.
func systemOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/system/requirements", id: "getSystemRequirements", tag: "system",
			summary: "Check local machine readiness (git, tmux, agent harness, gh)",
			resps: []respUnit{
				{http.StatusOK, controllers.SystemRequirementsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/system/github-auth", id: "getGitHubAuthRequirement", tag: "system",
			summary: "Check advisory GitHub CLI authentication without blocking startup",
			resps: []respUnit{
				{http.StatusOK, controllers.GitHubAuthRequirementResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/system/github-auth/terminal", id: "openGitHubAuthTerminal", tag: "system",
			summary: "Open a trusted terminal running the GitHub CLI login flow",
			resps: []respUnit{
				{http.StatusCreated, controllers.ShellTerminalEnvelope{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/system/install/{target}", id: "startSystemInstall", tag: "system",
			summary:    "Start (or return the already-running) install job for a fixed system target",
			pathParams: []any{controllers.InstallTargetParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.StartInstallResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/system/install/{target}", id: "getSystemInstallStatus", tag: "system",
			summary:    "Get the current or last known install job status for a system target",
			pathParams: []any{controllers.InstallTargetParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.InstallStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

func browserOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/browser/status", id: "getBrowserStatus", tag: "browser",
			summary:    "Check whether the desktop browser runtime is connected for a session",
			pathParams: []any{controllers.BrowserStatusQuery{}, controllers.BrowserCapabilityHeader{}},
			resps: []respUnit{
				{http.StatusOK, controllers.BrowserStatusResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/browser/commands", id: "executeBrowserCommand", tag: "browser",
			summary:    "Execute a target-scoped command in a session's desktop browser",
			pathParams: []any{controllers.BrowserCapabilityHeader{}},
			reqBody:    controllers.BrowserCommandRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.BrowserCommandResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusUnprocessableEntity, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusGatewayTimeout, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}

func awakeOperations() []operation {
	common := func(method, path, id string, body any, params []any) operation {
		return operation{method: method, path: path, id: id, tag: "settings", summary: "Manage desktop Stay Awake", reqBody: body, pathParams: params, resps: []respUnit{{http.StatusOK, domain.AwakeState{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}}}
	}
	return []operation{
		common(http.MethodGet, "/api/v1/awake", "getAwake", nil, nil),
		common(http.MethodGet, "/api/v1/awake/observations", "observeAwake", nil, nil),
		common(http.MethodPost, "/api/v1/awake/sessions", "startAwake", domain.StartAwakeRequest{}, nil),
		common(http.MethodPost, "/api/v1/awake/sessions/{awakeId}/end", "endAwake", nil, []any{controllers.AwakeIDParam{}}),
		common(http.MethodPost, "/api/v1/awake/sessions/{awakeId}/extend", "extendAwake", controllers.ExtendAwakeRequest{}, []any{controllers.AwakeIDParam{}}),
		common(http.MethodPut, "/api/v1/awake/settings", "setAwakeSettings", domain.AwakeSettings{}, nil),
		common(http.MethodPut, "/api/v1/awake/rules", "setAwakeRules", controllers.AwakeRulesRequest{}, nil),
		common(http.MethodPost, "/api/v1/awake/low-power/authorize", "authorizeAwakeLowPower", nil, nil),
		common(http.MethodPost, "/api/v1/awake/low-power/revoke", "revokeAwakeLowPower", nil, nil),
		common(http.MethodPost, "/api/v1/awake/stop", "stopAwake", nil, nil),
		common(http.MethodPost, "/api/v1/awake/resume", "resumeAwake", nil, nil),
		common(http.MethodPost, "/api/v1/awake/recover", "recoverAwake", nil, nil),
		common(http.MethodDelete, "/api/v1/awake/history", "clearAwakeHistory", nil, nil),
		common(http.MethodPost, "/api/v1/awake/downloads", "awakeDownloads", controllers.AwakeDownloadsRequest{}, nil),
	}
}
