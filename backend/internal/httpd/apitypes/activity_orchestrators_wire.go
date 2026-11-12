package apitypes

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// EndLaneRequest is the body of POST /api/v1/orchestrators/end. Name the lane
// orchestrator by SessionID, or by ProjectID plus Lane (a backlog item ref).
type EndLaneRequest struct {
	SessionID domain.SessionID `json:"sessionId,omitempty"`
	ProjectID domain.ProjectID `json:"projectId,omitempty"`
	Lane      string           `json:"lane,omitempty"`
	// DryRun runs the checks (including the unsaved-work refusal) and lists the
	// live workers that would move, changing nothing.
	DryRun bool `json:"dryRun,omitempty"`
}

// EndLaneResponse is the body of POST /api/v1/orchestrators/end.
type EndLaneResponse struct {
	// Removed is the deleted lane orchestrator.
	Removed domain.SessionID `json:"removed"`
	// Orchestrator is the default orchestrator the lane's workers moved to.
	Orchestrator domain.SessionID `json:"orchestrator"`
	// Workers are the live workers that moved to it.
	Workers []domain.SessionID `json:"workers"`
	// NotifyFailed are moved workers that did not receive the notice.
	NotifyFailed []domain.SessionID `json:"notifyFailed"`
}

// SetActivityRequest is the body of POST /api/v1/sessions/{sessionId}/activity.
// Event/ToolName/ToolUseID are optional correlation facts: which Tallpine hook
// sub-command produced the state and, for tool-use hooks, which tool call it
// concerns. Lifecycle uses them to clear a stale blocked state only when the
// specific approved tool finishes. Absent on old CLIs and on adapters whose
// payloads carry no tool identity — the signal then keeps its plain
// state-only semantics.
// AgentSessionID may arrive without State on metadata-only SessionStart hooks.
type SetActivityRequest struct {
	ObservedAt                   time.Time                           `json:"observedAt,omitempty" description:"Time the local hook process observed the event, before delivery to the daemon."`
	State                        string                              `json:"state,omitempty" enum:"active,idle,waiting_input,blocked,exited" description:"Agent activity state reported by an agent hook. Optional for metadata-only hooks."`
	Event                        string                              `json:"event,omitempty" description:"Tallpine hook sub-command that produced this state (e.g. post-tool-use)."`
	ToolName                     string                              `json:"toolName,omitempty" description:"Native tool name, for tool-use hook events."`
	ToolUseID                    string                              `json:"toolUseId,omitempty" description:"Native tool-use id, for tool-use hook events."`
	SubagentID                   string                              `json:"subagentId,omitempty" description:"Native child agent id for this hook event."`
	RunningSubagentIDs           *[]string                           `json:"runningSubagentIds,omitempty" description:"Running Claude subagent ids observed in a parent Stop hook; empty means none, absent means no snapshot."`
	AgentSessionID               string                              `json:"agentSessionId,omitempty" description:"Native agent session identifier used to resume its transcript."`
	LatestUserPrompt             string                              `json:"latestUserPrompt,omitempty" maxLength:"16384" description:"Latest real user prompt exposed by the provider hook."`
	LatestAssistantUpdate        string                              `json:"latestAssistantUpdate,omitempty" maxLength:"16384" description:"Latest assistant update exposed by the provider hook."`
	FinalAssistantMessage        string                              `json:"finalAssistantMessage,omitempty" maxLength:"16384" description:"The agent's final message of the turn, kept for terminal turn history only."`
	TurnUserRequest              string                              `json:"turnUserRequest,omitempty" maxLength:"16384" description:"The user's request for the turn, kept for terminal turn history only."`
	Harness                      string                              `json:"harness,omitempty" maxLength:"64" description:"The agent whose hook sent this signal. A signal from an agent the session no longer runs is ignored."`
	ConversationCheckpointOrigin domain.ConversationCheckpointOrigin `json:"conversationCheckpointOrigin,omitempty" enum:"human,coordination" description:"Whether the main-turn boundary came from a human or Tallpine coordination."`
	CoordinationID               string                              `json:"coordinationId,omitempty" description:"Opaque identity of a Tallpine-authored semantic prompt accepted by the native agent."`
	ProviderTurnID               string                              `json:"providerTurnId,omitempty" description:"Native main-turn identity reported by the hook, when supported."`
	SubmissionID                 string                              `json:"submissionId,omitempty" maxLength:"36" description:"Tallpine prompt-hook context correlation UUID, when supported."`
	TranscriptPath               string                              `json:"transcriptPath,omitempty" maxLength:"4096" description:"Read-only provider-native transcript path exposed by the hook."`
	LaunchID                     string                              `json:"launchId,omitempty" description:"Tallpine process generation that produced the signal."`
	Entrypoint                   string                              `json:"entrypoint,omitempty" description:"Provider process-kind marker (Claude's CLAUDE_CODE_ENTRYPOINT) of the process that fired the hook."`
	ProcessID                    int                                 `json:"processId,omitempty" minimum:"0" description:"Process id of the provider process that fired the hook (Claude's CLAUDE_PID)."`
	Usage                        *UsageHookMetadata                  `json:"usage,omitempty" description:"Provider transcript metadata used by the local usage pipeline."`
}

// SetReviewActivityRequest is the body of POST /api/v1/reviews/{reviewSessionID}/activity.
// AgentSessionID is the native reviewer conversation id used for reviewer
// restore. State is used for reviewer-pane live review presentation only; it
// does not mutate the worker session lifecycle row.
type SetReviewActivityRequest struct {
	State          string `json:"state,omitempty" enum:"active,idle,waiting_input,blocked,exited" description:"Reviewer activity state reported by a hook. Used for reviewer-pane live status, not worker session state."`
	Event          string `json:"event,omitempty" description:"Tallpine hook sub-command that produced this signal."`
	AgentSessionID string `json:"agentSessionId,omitempty" description:"Native reviewer session identifier used to resume its transcript."`
	LaunchID       string `json:"launchId,omitempty" description:"Tallpine process generation that produced the signal."`
}

// UsageHookMetadata is the transcript metadata carried by supported Claude
// Code and Codex hooks. It contains paths and identifiers only, never prompt or
// response content.
type UsageHookMetadata struct {
	Harness                domain.AgentHarness `json:"harness" enum:"claude-code,codex"`
	ProviderID             string              `json:"providerId,omitempty" description:"Canonical provider routing hint derived by the trusted local Claude hook."`
	TranscriptPath         string              `json:"transcriptPath,omitempty"`
	ModelID                string              `json:"modelId,omitempty"`
	SubagentID             string              `json:"subagentId,omitempty"`
	SubagentTranscriptPath string              `json:"subagentTranscriptPath,omitempty"`
}

// ClearedContextNoteResponse is the body of GET
// /api/v1/sessions/{sessionId}/cleared-context-note: the note an agent reads
// after its context was cleared, carrying the task, the workspace state and a
// record of the conversation.
type ClearedContextNoteResponse struct {
	Note string `json:"note"`
}
