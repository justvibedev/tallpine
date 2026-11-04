package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/service/systeminstall"
)

func agentOperations() []operation {
	return []operation{
		{
			method: http.MethodGet, path: "/api/v1/agents/auth-plans", id: "listAgentAuthPlans", tag: "agents",
			summary: "Return display-safe native authentication plans for supported agents",
			resps: []respUnit{
				{http.StatusOK, controllers.ListAgentAuthPlansResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/chat/unavailable", id: "listUnavailableChatAgents", tag: "conversations",
			summary:    "List which agents cannot run a chat because an adapter they need is not installed",
			pathParams: []any{controllers.UnavailableChatAgentsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.UnavailableChatAgentsResponse{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/chat/permission-modes", id: "listChatPermissionModes", tag: "conversations",
			summary:    "List the permission modes each agent can run a chat in, strictest first",
			pathParams: []any{controllers.UnavailableChatAgentsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ChatPermissionModesResponse{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/chat/prewarm", id: "prewarmChatAgent", tag: "conversations",
			summary:    "Run an agent's Chat readiness check in the background so a later switch to it skips it",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.PrewarmChatAgentResponse{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/auth", id: "startAgentAuth", tag: "agents",
			summary:    "Open the fixed native authentication flow for one agent",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusCreated, controllers.StartAgentAuthResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/logout", id: "logoutAgent", tag: "agents",
			summary:    "Open the fixed native logout flow for one agent",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusCreated, controllers.StartAgentAuthResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents", id: "listAgents", tag: "agents",
			summary: "Return cached supported and locally installed agent adapters",
			resps: []respUnit{
				{http.StatusOK, controllers.ListAgentsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/readiness", id: "getAgentReadiness", tag: "agents",
			summary: "Return cached normalized agent readiness without running native checks",
			resps: []respUnit{
				{http.StatusOK, controllers.AgentReadinessResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/readiness/ensure", id: "ensureAgentReadiness", tag: "agents",
			summary: "Ensure normalized readiness for selected agent adapters",
			reqBody: controllers.EnsureAgentReadinessRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentReadinessResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/codex/accounts", id: "getCodexAccounts", tag: "agents",
			summary: "Return cached Tallpine Codex accounts and active-account state",
			resps:   []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/ensure", id: "ensureCodexAccounts", tag: "agents",
			summary: "Discover Codex accounts and ensure authentication, capacity, and optional usage",
			reqBody: controllers.EnsureCodexAccountsRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/{accountId}/reset-credit/consume", id: "consumeCodexAccountResetCredit", tag: "agents",
			summary: "Consume one provider-reported Codex usage-limit reset credit", pathParams: []any{controllers.CodexAccountIDParam{}},
			reqBody: controllers.ConsumeCodexAccountResetCreditRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/{accountId}/login-terminal", id: "openCodexAccountReauthenticationTerminal", tag: "agents",
			summary: "Open native Codex sign-in for one retained account", pathParams: []any{controllers.CodexAccountIDParam{}},
			resps: []respUnit{{http.StatusAccepted, controllers.OpenCodexAccountLoginTerminalResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/{accountId}/logout", id: "logoutCodexAccount", tag: "agents",
			summary: "Log out one retained Codex account", pathParams: []any{controllers.CodexAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodDelete, path: "/api/v1/agents/codex/accounts/{accountId}", id: "deleteCodexAccount", tag: "agents",
			summary: "Delete one inactive signed-out Codex account", pathParams: []any{controllers.CodexAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/login-terminal", id: "openCodexAccountLoginTerminal", tag: "agents",
			summary: "Open an inline native login terminal for a new Tallpine Codex account",
			resps:   []respUnit{{http.StatusAccepted, controllers.OpenCodexAccountLoginTerminalResponse{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/login-operations/{operationId}/verify", id: "verifyCodexAccountLogin", tag: "agents",
			summary: "Verify one native Codex account login operation", pathParams: []any{controllers.CodexAccountLoginIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.CodexAccountLoginResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/accounts/login-operations/{operationId}/cancel", id: "cancelCodexAccountLogin", tag: "agents",
			summary: "Cancel one native Codex account login operation", pathParams: []any{controllers.CodexAccountLoginIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.CodexAccountLoginResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/codex/accounts/events", id: "streamCodexAccounts", tag: "agents",
			summary:      "Stream cached and live Codex account state",
			resps:        []respUnit{{http.StatusOK, controllers.CodexAccountsResponse{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
			contentTypes: map[int]string{http.StatusOK: "text/event-stream"},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/codex/account-switches", id: "startCodexAccountSwitch", tag: "agents",
			summary: "Start a global Tallpine Codex account switch", reqBody: controllers.StartCodexAccountSwitchRequest{},
			resps: []respUnit{{http.StatusAccepted, controllers.CodexAccountSwitchResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/codex/account-switches/{switchId}", id: "getCodexAccountSwitch", tag: "agents",
			summary: "Read one durable Codex account switch", pathParams: []any{controllers.CodexAccountSwitchIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.CodexAccountSwitchResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts", id: "listAgentAccounts", tag: "agents",
			summary: "List Claude Code and Codex accounts with their limits", pathParams: []any{controllers.AgentAccountListQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.AgentAccountsResponse{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts/usage", id: "getAgentAccountUsage", tag: "agents",
			summary: "Sum each account's model usage in Tallpine sessions", pathParams: []any{controllers.AgentAccountUsageQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.AgentAccountUsageResponse{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/login", id: "startAgentAccountLogin", tag: "agents",
			summary: "Add an account by running the provider's own sign-in in a terminal",
			reqBody: controllers.StartAgentAccountLoginRequest{},
			resps:   []respUnit{{http.StatusAccepted, controllers.StartAgentAccountLoginResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/login/{operationId}/verify", id: "verifyAgentAccountLogin", tag: "agents",
			summary: "Check a sign-in and save the account when it finished", pathParams: []any{controllers.AgentAccountLoginIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.VerifyAgentAccountLoginResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/login/{operationId}/cancel", id: "cancelAgentAccountLogin", tag: "agents",
			summary: "Stop a sign-in and remove its unsaved folder", pathParams: []any{controllers.AgentAccountLoginIDParam{}},
			resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts/sessions/{sessionId}", id: "getSessionAgentAccounts", tag: "agents",
			summary: "Read the account a session uses per provider", pathParams: []any{controllers.AgentAccountSessionIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.SessionAgentAccountsResponse{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPut, path: "/api/v1/agents/accounts/sessions/{sessionId}", id: "switchSessionAgentAccount", tag: "agents",
			summary: "Start moving a session to another account through the durable controller handoff", pathParams: []any{controllers.AgentAccountSessionIDParam{}},
			reqBody: controllers.SwitchSessionAgentAccountRequest{},
			resps:   []respUnit{{http.StatusAccepted, controllers.StartSessionInterfaceTransitionResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/{accountId}/login", id: "signInAgentAccountAgain", tag: "agents",
			summary: "Sign a saved account in again, in its own folder", pathParams: []any{controllers.AgentAccountIDParam{}},
			resps: []respUnit{{http.StatusAccepted, controllers.StartAgentAccountLoginResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/{accountId}/default", id: "setDefaultAgentAccount", tag: "agents",
			summary: "Make an account the default for new sessions of its provider", pathParams: []any{controllers.AgentAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.AgentAccountsResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPut, path: "/api/v1/agents/accounts/{accountId}/label", id: "setAgentAccountLabel", tag: "agents",
			summary: "Name a saved account so entries sharing an email can be told apart", pathParams: []any{controllers.AgentAccountIDParam{}},
			reqBody: controllers.SetAgentAccountLabelRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.AgentAccountsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts/{accountId}/adopt/preview", id: "previewAgentAccountAdoption", tag: "agents",
			summary: "List the live sessions that Use for all sessions would move to an account", pathParams: []any{controllers.AgentAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.AccountAdoptionPreviewResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/{accountId}/adopt", id: "adoptAgentAccount", tag: "agents",
			summary: "Make an account the default and move every live session of its provider to it", pathParams: []any{controllers.AgentAccountIDParam{}},
			reqBody: controllers.AdoptAgentAccountRequest{},
			resps:   []respUnit{{http.StatusAccepted, controllers.AccountAdoptionResponse{}}, {http.StatusOK, controllers.AccountAdoptionResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts/{accountId}/adoptions/latest", id: "getLatestAgentAccountAdoption", tag: "agents",
			summary: "Read the newest Use for all sessions batch of an account", pathParams: []any{controllers.AgentAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.AccountAdoptionResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/accounts/{accountId}/adoptions/{batchId}", id: "getAgentAccountAdoption", tag: "agents",
			summary: "Read one Use for all sessions batch with each session's outcome", pathParams: []any{controllers.AgentAccountAdoptionIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.AccountAdoptionResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/accounts/{accountId}/reset-credit", id: "consumeAgentAccountResetCredit", tag: "agents",
			summary: "Spend one Codex usage-limit reset of an account", pathParams: []any{controllers.AgentAccountIDParam{}},
			reqBody: controllers.ConsumeAgentAccountResetCreditRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.ConsumeAgentAccountResetCreditResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodDelete, path: "/api/v1/agents/accounts/{accountId}", id: "removeAgentAccount", tag: "agents",
			summary: "Sign an account out and remove it", pathParams: []any{controllers.AgentAccountIDParam{}},
			resps: []respUnit{{http.StatusOK, controllers.AgentAccountsResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/refresh", id: "refreshAgents", tag: "agents",
			summary: "Refresh the cached local agent adapter catalog",
			resps: []respUnit{
				{http.StatusOK, controllers.RefreshAgentsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/probe", id: "probeAgent", tag: "agents",
			summary:    "Ensure launch-fresh readiness for one agent adapter",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ProbeAgentResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/installers", id: "listAgentInstallers", tag: "agents",
			summary: "Resolve the safe installation plan for every supported agent harness",
			resps: []respUnit{
				{http.StatusOK, controllers.AgentInstallerCatalogResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/{agent}/update-advisory", id: "getAgentUpdateAdvisory", tag: "agents",
			summary:    "Compare an installed harness version with its known package source",
			pathParams: []any{controllers.AgentIDParam{}, controllers.AgentUpdateAdvisoryQuery{}},
			resps: []respUnit{
				{http.StatusOK, systeminstall.UpdateAdvisory{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/install", id: "startAgentInstall", tag: "agents",
			summary:    "Start an asynchronous install, reinstall, or update for one fixed agent harness",
			pathParams: []any{controllers.AgentIDParam{}},
			reqBody:    controllers.StartAgentInstallRequest{}, optionalReqBody: true,
			resps: []respUnit{
				{http.StatusAccepted, controllers.AgentInstallResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/install/cancel", id: "cancelAgentInstall", tag: "agents",
			summary:    "Cancel an update that is waiting for the harness's sessions to be idle",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentInstallResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/install-jobs", id: "listAgentInstallJobs", tag: "agents",
			summary: "Return the latest durable install job for every agent harness",
			resps: []respUnit{
				{http.StatusOK, controllers.AgentInstallJobsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/verify", id: "verifyAgentInstall", tag: "agents",
			summary:    "Verify an installed harness without reinstalling or probing authentication",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusAccepted, controllers.AgentInstallResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/{agent}/install", id: "getAgentInstallStatus", tag: "agents",
			summary:    "Get the current or last install job for one agent harness",
			pathParams: []any{controllers.AgentIDParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentInstallResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/agents/{agent}/models", id: "getAgentModels", tag: "agents",
			summary:    "Return the cached model picker for one agent, discovering it on first use",
			pathParams: []any{controllers.AgentIDParam{}, controllers.AgentModelsQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentModelsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/agents/{agent}/models/refresh", id: "refreshAgentModels", tag: "agents",
			summary:    "Refresh and cache the model picker for one agent",
			pathParams: []any{controllers.AgentIDParam{}, controllers.AgentModelsRefreshQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentModelsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
