package activitydispatch

import (
	"encoding/json"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/textutil"
)

const (
	maxHookInteractionLen = 16 << 10
	maxHookTranscriptPath = 4096
)

func hookConversationFacts(agent domain.AgentHarness, event string, payload []byte) Conversation {
	payload = normalizeHookPayload(payload)
	var p struct {
		Prompt               string `json:"prompt"`
		TurnID               string `json:"turn_id"`
		PromptID             string `json:"prompt_id"`
		UserPrompt           string `json:"user_prompt"`
		UserPromptCamel      string `json:"userPrompt"`
		LastAssistantMessage string `json:"last_assistant_message"`
		LastAssistantCamel   string `json:"lastAssistantMessage"`
		PromptResponse       string `json:"prompt_response"`
		PromptResponseCamel  string `json:"promptResponse"`
		TranscriptPath       string `json:"transcript_path"`
		TranscriptPathCamel  string `json:"transcriptPath"`
		SubagentID           string `json:"agent_id"`
		SessionID            string `json:"session_id"`
	}
	decodeErr := json.Unmarshal(payload, &p)
	observedPrompt := textutil.FirstTrimmed(p.Prompt, p.UserPrompt, p.UserPromptCamel)
	var userPrompt, assistant, finalMessage, turnID string
	origin := domain.ConversationCheckpointOriginUnknown
	// Conversation checkpoints are trusted only at the main-turn boundaries that
	// own each fact. Several Claude payloads repeat prompt/assistant aliases on
	// unrelated events, and SubagentStop uses the same shape as the main Stop.
	// Treating those copies as current main-thread facts can permanently make an
	// otherwise healthy provider replay look incomplete.
	if strings.TrimSpace(p.SubagentID) == "" {
		if event == "user-prompt-submit" || event == "stop" {
			switch agent {
			case domain.HarnessCodex:
				turnID = strings.TrimSpace(p.TurnID)
			case domain.HarnessClaudeCode:
				// Queued submissions reuse the executing prompt's ID. Preserve it as
				// native evidence; only the adapter can resolve its actual ancestry.
				turnID = strings.TrimSpace(p.PromptID)
			}
			if len(turnID) > maxActivityMetaLen || domain.SanitizeControlChars(turnID) != turnID {
				turnID = ""
			}
		}
		switch event {
		case "user-prompt-submit":
			userPrompt = observedPrompt
			origin = domain.ConversationCheckpointOriginHuman
		case "stop":
			// Claude's hooks report this field on Stop. Similar-looking fields
			// from Codex do not carry the same main-turn guarantee and must not
			// become hard replay checkpoints.
			if agent == domain.HarnessClaudeCode {
				assistant = p.LastAssistantMessage
			}
			finalMessage = textutil.FirstTrimmed(p.LastAssistantMessage, p.LastAssistantCamel, p.PromptResponse, p.PromptResponseCamel)
		}
	}
	// Tallpine's own handoff request and continuation kickoff are coordination turns,
	// not the latest real user instruction. They remain in provider history but
	// must not overwrite deterministic user intent.
	if isTallpineCoordinationMessage(observedPrompt) {
		userPrompt = ""
		assistant = ""
		finalMessage = ""
		if event == "user-prompt-submit" || event == "stop" {
			origin = domain.ConversationCheckpointOriginCoordination
		}
	}
	if event == "stop" && agent == domain.HarnessKiro && decodeErr == nil && strings.TrimSpace(finalMessage) == "" {
		// Kiro v3's Stop payload carries no reply text; read it from the session log.
		finalMessage = kiroFinalReply(p.SessionID)
	}
	// The terminal turn history takes a main turn's text from every agent, under
	// whichever name its hook uses, but only from a payload that decoded cleanly.
	// A worker report or session send Tallpine delivered is a request in that history,
	// as it is in Chat; Tallpine's handoff and continuation prompts are not.
	turnRequest := userPrompt
	if body, delivered := domain.CoordinationDeliveryBody(observedPrompt); delivered &&
		event == "user-prompt-submit" && strings.TrimSpace(p.SubagentID) == "" {
		turnRequest = body
	}
	if decodeErr != nil {
		turnRequest, finalMessage = "", ""
	}
	coordinationID, _ := domain.CoordinationDeliveryID(observedPrompt)
	return Conversation{
		ProviderTurnID:        turnID,
		LatestUserPrompt:      capHookText(userPrompt, maxHookInteractionLen),
		LatestAssistantUpdate: capHookText(assistant, maxHookInteractionLen),
		FinalAssistantMessage: capHookText(finalMessage, maxHookInteractionLen),
		TurnUserRequest:       capHookText(turnRequest, maxHookInteractionLen),
		CheckpointOrigin:      origin,
		CoordinationID:        coordinationID,
		TranscriptPath:        capHookText(textutil.FirstTrimmed(p.TranscriptPath, p.TranscriptPathCamel), maxHookTranscriptPath),
	}
}

func isTallpineCoordinationMessage(value string) bool {
	value = strings.TrimSpace(value)
	_, reportDelivery := domain.CoordinationDeliveryID(value)
	return reportDelivery || strings.HasPrefix(value, "<ao-handoff-request") ||
		strings.HasPrefix(value, "Tallpine transferred the previous agent's context in hidden system instructions.") ||
		strings.HasPrefix(value, "AO transferred the previous agent's context in hidden system instructions.")
}

func capHookText(value string, limit int) string {
	value = domain.SanitizeControlChars(strings.TrimSpace(value))
	if limit <= 0 || len(value) <= limit {
		return value
	}
	const marker = "\n[... truncated by Tallpine ...]\n"
	budget := limit - len(marker)
	if budget <= 0 {
		return ""
	}
	head := budget / 2
	tail := budget - head
	return strings.ToValidUTF8(string([]byte(value)[:head])+marker+string([]byte(value)[len(value)-tail:]), "?")
}
