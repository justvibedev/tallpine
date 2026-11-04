package controllers

import (
	"tallpine.eu/tallpine/backend/internal/service/agentauth"
)

// HTTP response envelopes for the projects surface — the SINGLE definition of
// each wire shape. The handlers encode these (envelope.WriteJSON), and
// apispec.Build reflects these same types into openapi.yaml, so the served
// contract and the generated spec can't disagree. The request side needs no
// wrappers: handlers decode the body straight into the project commands
// (projectsvc.AddInput), which apispec also reflects.

// ProjectIDParam is the {id} path parameter shared by the /projects/{id}
// routes. Handlers read it via chi.URLParam (see projectID); it is declared here
// so every wire input/output shape has one home, and apispec.Build reflects it
// as the path parameter.
type ProjectIDParam struct {
	ID string `path:"id" description:"Project identifier (registry key)."`
}

// TaskPreparationTokenParam identifies an unclaimed speculative task workspace.
type TaskPreparationTokenParam struct {
	Token string `path:"token" description:"Opaque speculative task-worktree token."`
}

// AgentIDParam is the {agent} path parameter for one-agent catalog probes.
type AgentIDParam struct {
	Agent string `path:"agent" description:"Agent adapter identifier."`
}

// UnavailableChatAgentsQuery names the agents to check.
type UnavailableChatAgentsQuery struct {
	Agents string `query:"agents" description:"Comma-separated agent adapter identifiers."`
}

// UnavailableChatAgentsResponse lists agents whose chat adapter is missing.
type UnavailableChatAgentsResponse struct {
	Agents []UnavailableChatAgent `json:"agents"`
}

// UnavailableChatAgent is one agent that cannot run a chat, and why.
type UnavailableChatAgent struct {
	Agent  string `json:"agent"`
	Reason string `json:"reason"`
}

// ChatPermissionModesResponse lists, for each asked agent that can run a chat,
// the permission modes it runs in.
type ChatPermissionModesResponse struct {
	Agents []ChatAgentPermissionModes `json:"agents"`
}

// ChatAgentPermissionModes is one agent's permission modes, strictest first; a
// switch to it from a chat in another mode starts it in the first.
type ChatAgentPermissionModes struct {
	Agent string   `json:"agent"`
	Modes []string `json:"modes"`
}

// PrewarmChatAgentResponse acknowledges a background Chat readiness check.
type PrewarmChatAgentResponse struct {
	Agent string `json:"agent" description:"Agent adapter identifier whose Chat check was started."`
}

// ListAgentAuthPlansResponse is the display-safe authentication catalog.
type ListAgentAuthPlansResponse struct {
	Plans []agentauth.Plan `json:"plans"`
}

// StartAgentAuthResponse returns the native terminal opened for authentication.
type StartAgentAuthResponse struct {
	AgentID       string                `json:"agentId"`
	Action        agentauth.Action      `json:"action"`
	Guidance      string                `json:"guidance,omitempty"`
	TerminalInput string                `json:"terminalInput,omitempty"`
	Terminal      ShellTerminalResponse `json:"terminal"`
}

// CodexAccountIDParam documents a Codex account route identifier.
type CodexAccountIDParam struct {
	AccountID string `path:"accountId" description:"Tallpine Codex account identifier."`
}

// CodexAccountLoginIDParam documents a Codex login operation route identifier.
type CodexAccountLoginIDParam struct {
	OperationID string `path:"operationId" description:"In-memory Codex account login operation identifier."`
}

// CodexAccountSwitchIDParam documents a durable Codex account switch identifier.
type CodexAccountSwitchIDParam struct {
	SwitchID string `path:"switchId" description:"Durable Codex account switch identifier."`
}
