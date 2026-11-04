package activitydispatch

import (
	"bytes"
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// maxActivityMetaLen caps the correlation fields lifted from a native hook
// payload before they go on the wire — they are ids/names, anything longer is
// garbage and gets dropped rather than truncated (a truncated id would never
// match its pre/post counterpart).
const maxActivityMetaLen = 256

// UsageMetadata is the provider-native usage evidence a hook payload carries.
// ProviderID is not derived from the payload: the hook process fills it from
// its own environment (pricing.ClaudeRouteFromEnv).
type UsageMetadata struct {
	Harness                string `json:"harness"`
	ProviderID             string `json:"providerId,omitempty"`
	TranscriptPath         string `json:"transcriptPath,omitempty"`
	ModelID                string `json:"modelId,omitempty"`
	SubagentID             string `json:"subagentId,omitempty"`
	SubagentTranscriptPath string `json:"subagentTranscriptPath,omitempty"`
}

// Conversation is the turn text and checkpoint evidence lifted from a payload.
type Conversation struct {
	LatestUserPrompt      string                              `json:"latestUserPrompt"`
	LatestAssistantUpdate string                              `json:"latestAssistantUpdate"`
	FinalAssistantMessage string                              `json:"finalAssistantMessage"`
	TurnUserRequest       string                              `json:"turnUserRequest"`
	CheckpointOrigin      domain.ConversationCheckpointOrigin `json:"checkpointOrigin"`
	CoordinationID        string                              `json:"coordinationId"`
	ProviderTurnID        string                              `json:"providerTurnId"`
	TranscriptPath        string                              `json:"transcriptPath"`
}

// HookFacts is everything `tallpine hooks <agent> <event>` derives from a
// native hook payload, independent of the daemon wire and of the hook
// process's environment.
type HookFacts struct {
	// Event is the hook sub-command, rewritten to subagent-spawn for a Codex
	// spawn_agent tool result.
	Event          string               `json:"event"`
	State          domain.ActivityState `json:"state"`
	HasActivity    bool                 `json:"hasActivity"`
	Reportable     bool                 `json:"reportable"`
	AgentSessionID string               `json:"agentSessionId"`
	// NativeSessionID is the payload's session id exactly as the harness
	// reported it. Reviewer hooks forward it as-is; AgentSessionID additionally
	// drops the child id a DeepSeek subagent event carries.
	NativeSessionID    string         `json:"nativeSessionId"`
	Usage              *UsageMetadata `json:"usage"`
	SubagentID         string         `json:"subagentId"`
	RunningSubagentIDs *[]string      `json:"runningSubagentIds"`
	ToolName           string         `json:"toolName"`
	ToolUseID          string         `json:"toolUseId"`
	PayloadLaunchID    string         `json:"payloadLaunchId"`
	Conversation       Conversation   `json:"conversation"`
}

// Facts derives the hook facts for one agent event. Reportable is false for an
// event that carries neither activity nor resumable session metadata.
func Facts(agent, event string, payload []byte) HookFacts {
	harness := domain.AgentHarness(agent)
	state, hasActivity := Derive(agent, event, payload)
	agentSessionID, nativeSessionID := "", ""
	if SupportsHarness(harness) {
		agentSessionID = hookAgentSessionID(payload)
		nativeSessionID = agentSessionID
		// dsh's hooks plugin stamps SubagentStart/Stop with the CHILD agent's
		// session_id; forwarding it would replace the main session's resume id.
		if harness == domain.HarnessDeepSeek && (event == "subagent-start" || event == "subagent-stop") {
			agentSessionID = ""
		}
	}
	usage := hookUsageMetadata(agent, payload)
	var subagentID string
	var running *[]string
	switch harness {
	case domain.HarnessClaudeCode, domain.HarnessDeepSeek:
		// dsh's hooks plugin emits Claude-shaped SubagentStart/Stop payloads.
		subagentID, running = claudeSubagentFacts(event, payload)
	case domain.HarnessCodex:
		subagentID = codexSubagentID(event, payload)
		if event == "post-tool-use" {
			if spawnID := codexSpawnToolUseID(payload); spawnID != "" {
				subagentID = spawnID
				event = "subagent-spawn"
			}
		}
	}
	toolName, toolUseID := activityMeta(payload)
	var conversation Conversation
	switch harness {
	case domain.HarnessClaudeCode, domain.HarnessCodex:
		conversation = hookConversationFacts(harness, event, payload)
	}
	// Every agent's turn text feeds the terminal turn history. It never becomes a replay checkpoint.
	history := hookConversationFacts(harness, event, payload)
	conversation.TurnUserRequest, conversation.FinalAssistantMessage = history.TurnUserRequest, history.FinalAssistantMessage
	return HookFacts{
		Event:              event,
		State:              state,
		HasActivity:        hasActivity,
		Reportable:         hasActivity || agentSessionID != "" || usage != nil || subagentID != "",
		AgentSessionID:     agentSessionID,
		NativeSessionID:    nativeSessionID,
		Usage:              usage,
		SubagentID:         subagentID,
		RunningSubagentIDs: running,
		ToolName:           toolName,
		ToolUseID:          toolUseID,
		PayloadLaunchID:    hookLaunchID(payload),
		Conversation:       conversation,
	}
}

// activityMeta extracts the tool-use correlation facts from a native hook
// payload. The field names are shared vocabulary across agent CLIs that emit
// them (claude-code's PreToolUse/PostToolUse/PostToolUseFailure and
// PermissionRequest payloads); adapters whose payloads lack them yield empty
// strings and the signal degrades to today's state-only form.
func activityMeta(payload []byte) (toolName, toolUseID string) {
	payload = normalizeHookPayload(payload)
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolUseID string `json:"tool_use_id"`
	}
	_ = json.Unmarshal(payload, &p)
	if len(p.ToolName) > maxActivityMetaLen {
		p.ToolName = ""
	}
	if len(p.ToolUseID) > maxActivityMetaLen {
		p.ToolUseID = ""
	}
	return p.ToolName, p.ToolUseID
}

func validSubagentID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxActivityMetaLen || domain.SanitizeControlChars(id) != id {
		return ""
	}
	return id
}

// normalizeHookPayload strips a leading UTF-8 BOM so payloads re-encoded by a
// hook wrapper (notably Windows PowerShell, whose pipeline writes UTF-16 text
// that surfaces to the child with a BOM prefix) still decode as JSON.
func normalizeHookPayload(payload []byte) []byte {
	return bytes.TrimPrefix(payload, []byte("\xef\xbb\xbf"))
}

// hookAgentSessionID extracts the native resume handle shared by Codex, Claude
// Code, Kiro, and other hook payloads. It is independent of
// activity derivation because SessionStart is intentionally metadata-only for
// harnesses where process startup is not proof that a turn is active.
func hookAgentSessionID(payload []byte) string {
	payload = normalizeHookPayload(payload)
	var p struct {
		SessionID           string `json:"session_id"`
		SessionIDCamel      string `json:"sessionId"`
		ConversationID      string `json:"conversation_id"`
		ConversationIDCamel string `json:"conversationId"`
	}
	_ = json.Unmarshal(payload, &p)
	id := strings.TrimSpace(p.SessionID)
	if id == "" {
		id = strings.TrimSpace(p.SessionIDCamel)
	}
	if id == "" {
		id = strings.TrimSpace(p.ConversationID)
	}
	if id == "" {
		id = strings.TrimSpace(p.ConversationIDCamel)
	}
	if len(id) > maxActivityMetaLen {
		return ""
	}
	return id
}

// hookLaunchID extracts the runtime launch id a plugin embeds in its payload.
// It is a fallback for TALLPINE_RUNTIME_LAUNCH_ID when child-process env inheritance
// is trimmed by the agent runtime.
func hookLaunchID(payload []byte) string {
	payload = normalizeHookPayload(payload)
	var p struct {
		LaunchID      string `json:"launch_id"`
		LaunchIDCamel string `json:"launchId"`
	}
	_ = json.Unmarshal(payload, &p)
	id := strings.TrimSpace(p.LaunchID)
	if id == "" {
		id = strings.TrimSpace(p.LaunchIDCamel)
	}
	if len(id) > maxActivityMetaLen {
		return ""
	}
	return id
}

// hookUsageMetadata extracts provider-native usage metadata. It deliberately
// decodes separately from conversation facts because hook producers may emit
// a malformed field in one projection while the other remains useful.
func hookUsageMetadata(agent string, payload []byte) *UsageMetadata {
	payload = normalizeHookPayload(payload)
	harness := domain.AgentHarness(agent)
	if harness != domain.HarnessClaudeCode && harness != domain.HarnessCodex {
		return nil
	}
	var native struct {
		TranscriptPath         string `json:"transcript_path"`
		Model                  string `json:"model"`
		SubagentID             string `json:"agent_id"`
		SubagentTranscriptPath string `json:"agent_transcript_path"`
	}
	if json.Unmarshal(payload, &native) != nil {
		return nil
	}
	meta := &UsageMetadata{
		Harness:                agent,
		TranscriptPath:         strings.TrimSpace(native.TranscriptPath),
		ModelID:                strings.TrimSpace(native.Model),
		SubagentID:             strings.TrimSpace(native.SubagentID),
		SubagentTranscriptPath: strings.TrimSpace(native.SubagentTranscriptPath),
	}
	if meta.TranscriptPath == "" && meta.SubagentTranscriptPath == "" && meta.ModelID == "" {
		return nil
	}
	return meta
}
