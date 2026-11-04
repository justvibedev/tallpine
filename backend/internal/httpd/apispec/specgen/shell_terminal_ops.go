package specgen

import (
	"net/http"

	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// shellTerminalOperations describes the standalone shell terminal surface:
// shells the user opens by hand, with no agent session behind them.
func shellTerminalOperations() []operation {
	ops := append([]operation{}, shellTerminalOperationsPart1()...)
	ops = append(ops, shellTerminalOperationsPart2()...)
	ops = append(ops, shellTerminalOperationsPart3()...)
	return ops
}

func shellTerminalOperationsPart1() []operation {
	return []operation{
		{method: http.MethodPatch, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/label", id: "renameSessionSideChat", tag: "conversations", pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}}, reqBody: controllers.SideLabelRequest{}, resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}}},
		{method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/turns/{turnId}/cancel", id: "cancelSessionSideQuestion", tag: "conversations", pathParams: []any{controllers.SessionIDParam{}, controllers.SideChatIDParam{}, controllers.ConversationTurnIDParam{}}, resps: []respUnit{{http.StatusNoContent, nil}, {http.StatusConflict, envelope.APIError{}}}},
		{
			method: http.MethodGet, path: "/api/v1/settings", id: "getSettings", tag: "settings",
			summary: "Read the daemon-owned user preferences",
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/harness-defaults/{agent}", id: "updateHarnessDefault", tag: "settings",
			summary:    "Choose which models one harness's pickers offer",
			pathParams: []any{controllers.AgentIDParam{}},
			reqBody:    controllers.UpdateHarnessDefaultRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/session-interface", id: "updateSessionInterface", tag: "settings",
			summary: "Choose the default interface for new sessions",
			reqBody: controllers.UpdateSessionInterfaceRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/chat-hibernation", id: "updateChatHibernation", tag: "settings",
			summary: "Turn idle Chat hibernation on or off for this machine",
			reqBody: controllers.UpdateChatHibernationRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/version-refresh", id: "updateVersionRefresh", tag: "settings",
			summary: "Choose whether idle open sessions move to newly installed harness releases",
			reqBody: controllers.UpdateVersionRefreshRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/dynamic-orchestrator-context", id: "updateDynamicOrchestratorContext", tag: "settings",
			summary: "Choose whether orchestrators may raise their compaction window while they research",
			reqBody: controllers.UpdateDynamicOrchestratorContextRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/workflow-defaults", id: "updateWorkflowDefaults", tag: "settings",
			summary: "Change the app-wide workflow defaults projects inherit",
			reqBody: controllers.UpdateWorkflowDefaultsRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/artifacts/storage", id: "getArtifactStorage", tag: "settings",
			summary: "Report each session's artifact disk usage and which sessions a retention action may apply to", pathParams: []any{controllers.ArtifactStorageQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.ArtifactStorageResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/artifacts/storage/retention", id: "retainArtifacts", tag: "settings",
			summary: "Archive or delete the artifacts of sessions terminated longer ago than a window, after the user confirmed",
			reqBody: controllers.ArtifactRetentionRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.ArtifactRetentionResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/storage", id: "getStorage", tag: "settings",
			summary: "List everything Tallpine and its agents keep on disk, with size, age, owner and state; serves the cached measurement and refreshes it in the background", pathParams: []any{controllers.StorageQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.StorageResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/storage/delete", id: "deleteStorageEntry", tag: "settings",
			summary: "Remove one storage entry; live sessions, orchestrators and the newest rollback point are refused",
			reqBody: controllers.StorageDeleteRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.StorageDeleteResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/storage/cleanup", id: "cleanupStorage", tag: "settings",
			summary: "Clean up everything safe: ended sessions' worktrees, scratch and caches, the unused Go build cache, and rollback points beyond the newest",
			reqBody: controllers.StorageCleanupRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.CleanupRunReport{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/health/silent-failures", id: "getSilentFailures", tag: "settings",
			summary: "List Chat sessions with no attached agent, report batches stuck past their delivery time, and final-result notices overdue",
			resps:   []respUnit{{http.StatusOK, controllers.SilentFailuresResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/update", id: "getUpdateStatus", tag: "settings",
			summary: "Compare the installed commit with the newest origin/main commit that has a passing local CI row",
			resps:   []respUnit{{http.StatusOK, controllers.UpdateStatusResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/update", id: "startUpdate", tag: "settings",
			summary: "Start the detached update job for a CI-passed commit; the app and daemon restart",
			reqBody: controllers.StartUpdateRequest{},
			resps:   []respUnit{{http.StatusAccepted, controllers.UpdateResult{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusUnprocessableEntity, envelope.APIError{}}, {http.StatusServiceUnavailable, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/cleanup", id: "getCleanupStatus", tag: "settings",
			summary: "Report the automatic cleanup policy, the latest real and dry runs, and when the next passes are due",
			resps:   []respUnit{{http.StatusOK, controllers.CleanupStatusResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/cleanup/policy", id: "updateCleanupPolicy", tag: "settings",
			summary: "Change the automatic cleanup policy; only the fields sent change",
			reqBody: controllers.UpdateCleanupPolicyRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.CleanupPolicy{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/cleanup/runs", id: "runCleanup", tag: "settings",
			summary: "Run a cleanup pass now, or a dry run that reports what a pass would remove",
			reqBody: controllers.CleanupRunRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.CleanupRunReport{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/cleanup/runs", id: "listCleanupRuns", tag: "settings",
			summary: "List recent cleanup runs and what each freed, newest first", pathParams: []any{controllers.CleanupRunsQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.CleanupRunsResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/schema", id: "getSettingsSchema", tag: "settings",
			summary: "List every registered setting: key, scope, type, default, bounds, environment overrides, MDM lockability and writers",
			resps:   []respUnit{{http.StatusOK, controllers.SettingsSchemaResponse{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/values", id: "getSettingsValues", tag: "settings",
			summary: "Read effective setting values, where each comes from, whether it is locked and who may change it; app-wide unless a project is given", pathParams: []any{controllers.SettingsValuesQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.SettingsValuesResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusForbidden, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/values", id: "updateSettingsValues", tag: "settings",
			summary: "Change settings by key in one request; every key is validated before anything is written, and the response holds the new effective values",
			reqBody: controllers.UpdateSettingsValuesRequest{},
			resps:   []respUnit{{http.StatusOK, controllers.SettingsValuesResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusForbidden, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/changes", id: "listSettingsChanges", tag: "settings",
			summary: "List recent settings changes and who made them, newest first; app-wide unless a project is given", pathParams: []any{controllers.SettingsChangesQuery{}},
			resps: []respUnit{{http.StatusOK, controllers.SettingsChangesResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusForbidden, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/settings/agent-policy", id: "getAgentPolicy", tag: "settings",
			summary: "Read the agent policy: rules, settings, effective values and which layer sets each",
			resps:   []respUnit{{http.StatusOK, controllers.AgentPolicyResponse{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/agent-policy", id: "updateAgentPolicy", tag: "settings",
			summary: "Change fields of the user layer of the agent policy; fields locked by managed config are refused",
			reqBody: controllers.UpdateAgentPolicyRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentPolicyResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/skills", id: "getProjectSkills", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "List every skill any agent convention offers for a project, its source, and which harnesses see it",
			resps:      []respUnit{{http.StatusOK, controllers.ProjectSkillsResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/skills/offers/{offerId}/apply", id: "applyProjectSkillOffer", tag: "projects",
			pathParams: []any{controllers.ProjectSkillOfferParam{}},
			summary:    "Create the files of one skills offer in the project checkout (uncommitted, never overwriting)",
			resps:      []respUnit{{http.StatusOK, controllers.ApplyProjectSkillOfferResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/github-account-check", id: "getProjectGitHubAccountCheck", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "Report which GitHub login the project's token actually authenticates as and whether it can push to the repo",
			resps:      []respUnit{{http.StatusOK, controllers.ProjectGitHubAccountCheckResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/default-branch-check", id: "getProjectDefaultBranchCheck", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "Check whether a branch is strictly ahead of and much newer than the branch new sessions start from (detection only)",
			resps:      []respUnit{{http.StatusOK, controllers.ProjectDefaultBranchCheckResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/skills/remove", id: "removeProjectSkill", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}}, reqBody: controllers.RemoveProjectSkillRequest{},
			summary: "Delete a link or copy the skills bridge created in the checkout (never the source skill)",
			resps:   []respUnit{{http.StatusOK, controllers.RemoveProjectSkillResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/skills/commit", id: "commitProjectSkills", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}}, reqBody: controllers.CommitProjectSkillsRequest{},
			summary: "Commit created instruction files and skill links to the branch new sessions start from, pushing when they start from the remote",
			resps:   []respUnit{{http.StatusOK, controllers.CommitProjectSkillsResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/projects/{id}/skills/push-branch", id: "pushProjectSkillsBranch", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}}, reqBody: controllers.PushProjectSkillsBranchRequest{},
			summary: "Push a skills commit as a new branch for a pull request (never forces)",
			resps:   []respUnit{{http.StatusOK, controllers.PushProjectSkillsBranchResponse{}}, {http.StatusBadRequest, envelope.APIError{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusConflict, envelope.APIError{}}, {http.StatusBadGateway, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodGet, path: "/api/v1/projects/{id}/agent-policy", id: "getProjectAgentPolicy", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "Read the agent policy as it applies to one project, with its overrides",
			resps:      []respUnit{{http.StatusOK, controllers.AgentPolicyResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPatch, path: "/api/v1/projects/{id}/agent-policy", id: "updateProjectAgentPolicy", tag: "projects",
			pathParams: []any{controllers.ProjectIDParam{}},
			summary:    "Change one project's agent policy overrides; a project may only tighten security settings",
			reqBody:    controllers.UpdateAgentPolicyRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.AgentPolicyResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/agent-policy", id: "getSessionAgentPolicy", tag: "sessions",
			summary:    "Read the agent policy decision a session's latest launch was made under",
			pathParams: []any{controllers.SessionIDParam{}},
			resps:      []respUnit{{http.StatusOK, controllers.SessionPolicyDecisionResponse{}}, {http.StatusNotFound, envelope.APIError{}}, {http.StatusInternalServerError, envelope.APIError{}}, {http.StatusNotImplemented, envelope.APIError{}}},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/github-credential", id: "issueGitHubCredential", tag: "sessions",
			summary:    "Issue a GitHub credential to a helper-mode agent for one use",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.GitHubCredentialRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.GitHubCredentialResponse{}},
				{http.StatusForbidden, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/commit", id: "commitSessionWorktree", tag: "sessions",
			summary:    "Stage and commit all changes in the session's own worktree (no push)",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SessionCommitRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SessionCommitResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPatch, path: "/api/v1/settings/storage-compaction", id: "updateStorageCompaction", tag: "settings",
			summary: "Turn storage compaction on or off and set how long provider events of terminated sessions are kept",
			reqBody: controllers.UpdateStorageCompactionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.SettingsResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/chat-view", id: "setSessionChatView", tag: "conversations",
			summary:    "Keep a viewed chat awake or release its view lease",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SetChatViewRequest{},
			resps: []respUnit{
				{http.StatusNoContent, nil},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodGet, path: "/api/v1/sessions/{sessionId}/conversation", id: "getSessionConversation", tag: "conversations",
			summary:    "Read a chat session's durable conversation",
			pathParams: []any{controllers.SessionIDParam{}, conversationSnapshotQuery{}},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationSnapshotResponse{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/prompt-suggestion", id: "suggestSessionConversationPrompt", tag: "conversations",
			summary:    "Suggest the next human message after a completed chat turn",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ConversationPromptSuggestionRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ConversationPromptSuggestionResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusTooManyRequests, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/conversation/messages", id: "sendSessionConversationMessage", tag: "conversations",
			summary:    "Send a message to a chat session's agent",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.SendConversationMessageRequest{},
			resps: []respUnit{
				{http.StatusAccepted, controllers.SendConversationMessageResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusNotFound, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPost, path: "/api/v1/sessions/{sessionId}/provider-switch", id: "switchSessionProvider", tag: "conversations",
			summary:    "Switch an idle Chat session to another provider",
			pathParams: []any{controllers.SessionIDParam{}},
			reqBody:    controllers.ProviderSwitchRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.ProviderSwitchResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusConflict, envelope.APIError{}},
				{http.StatusServiceUnavailable, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
				{http.StatusNotImplemented, envelope.APIError{}},
			},
		},
	}
}
