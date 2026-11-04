package activitydispatch

import (
	"encoding/json"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestHookConversationFactsNativeTurnIdentity(t *testing.T) {
	for _, tt := range []struct {
		name    string
		harness domain.AgentHarness
		event   string
		payload string
		want    string
	}{
		{"prompt", domain.HarnessCodex, "user-prompt-submit", `{"prompt":"continue","turn_id":"turn-1"}`, "turn-1"},
		{"stop", domain.HarnessCodex, "stop", `{"turn_id":"turn-1"}`, "turn-1"},
		{"subagent", domain.HarnessCodex, "stop", `{"turn_id":"turn-1","agent_id":"child"}`, ""},
		{"unrelated event", domain.HarnessCodex, "post-tool-use", `{"turn_id":"turn-1"}`, ""},
		{"other provider", domain.HarnessClaudeCode, "stop", `{"turn_id":"turn-1"}`, ""},
		{"Claude prompt", domain.HarnessClaudeCode, "user-prompt-submit", `{"prompt":"continue","prompt_id":"prompt-1"}`, "prompt-1"},
		{"Claude stop", domain.HarnessClaudeCode, "stop", `{"prompt_id":"prompt-1","turn_id":"unrelated"}`, "prompt-1"},
		{"Claude subagent", domain.HarnessClaudeCode, "stop", `{"prompt_id":"prompt-1","agent_id":"child"}`, ""},
		{"Claude unrelated event", domain.HarnessClaudeCode, "post-tool-use", `{"prompt_id":"prompt-1"}`, ""},
		{"Claude missing ID", domain.HarnessClaudeCode, "stop", `{}`, ""},
		{"Claude invalid ID", domain.HarnessClaudeCode, "stop", `{"prompt_id":"bad\u001b[0mid"}`, ""},
		{"Claude oversized ID", domain.HarnessClaudeCode, "stop", `{"prompt_id":"` + strings.Repeat("a", 257) + `"}`, ""},
		{"missing ID", domain.HarnessCodex, "stop", `{}`, ""},
		{"oversized ID", domain.HarnessCodex, "stop", `{"turn_id":"` + strings.Repeat("a", 257) + `"}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := hookConversationFacts(tt.harness, tt.event, []byte(tt.payload)).ProviderTurnID; got != tt.want {
				t.Fatalf("turn ID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHookConversationFactsCorrelatesAcceptedReportDelivery(t *testing.T) {
	prompt := domain.WrapReportDelivery("report-batch:abc123", "Reports since your previous turn:")
	payload := []byte(`{"prompt":` + mustJSONString(t, prompt) + `,"prompt_id":"native-turn"}`)
	got := hookConversationFacts(domain.HarnessClaudeCode, "user-prompt-submit", payload)
	if got.CheckpointOrigin != domain.ConversationCheckpointOriginCoordination ||
		got.CoordinationID != "report-batch:abc123" || got.LatestUserPrompt != "" {
		t.Fatalf("conversation facts = %+v", got)
	}
}

func TestHookConversationFactsTrustsOnlyDocumentedClaudeStopAssistantField(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{name: "documented snake case", payload: `{"last_assistant_message":"trusted"}`, want: "trusted"},
		{name: "camel case lookalike", payload: `{"lastAssistantMessage":"untrusted"}`},
		{name: "assistant snake case lookalike", payload: `{"assistant_message":"untrusted"}`},
		{name: "assistant camel case lookalike", payload: `{"assistantMessage":"untrusted"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hookConversationFacts(domain.HarnessClaudeCode, "stop", []byte(tt.payload))
			if got.LatestAssistantUpdate != tt.want {
				t.Fatalf("assistant checkpoint = %q, want %q", got.LatestAssistantUpdate, tt.want)
			}
		})
	}
}

func TestHookConversationFactsExcludesTallpineCoordinationUserTurns(t *testing.T) {
	for _, prompt := range []string{
		"<ao-handoff-request>\nprepare context",
		"<ao-handoff-request switch-id=\"switch-1\">\nprepare context",
		"Tallpine transferred the previous agent's context in hidden system instructions. Continue the unfinished action.",
	} {
		got := hookConversationFacts(domain.HarnessClaudeCode, "user-prompt-submit", []byte(`{"prompt":`+mustJSONString(t, prompt)+`,"lastAssistantMessage":"ok"}`))
		if got.LatestUserPrompt != "" {
			t.Fatalf("prompt %q was retained as real user intent", prompt)
		}
		if got.LatestAssistantUpdate != "" {
			t.Fatalf("assistant update = %q, want event-scoped empty value", got.LatestAssistantUpdate)
		}
	}
}

func TestHookMetadataAndConversationFactsTolerateMalformedOtherProjection(t *testing.T) {
	t.Run("malformed usage retains conversation", func(t *testing.T) {
		payload := []byte(`{"prompt":"continue investigating","last_assistant_message":"updated","transcriptPath":"/tmp/conversation.jsonl","model":false}`)
		conversation := hookConversationFacts(domain.HarnessClaudeCode, "stop", payload)
		if conversation.LatestUserPrompt != "" || conversation.LatestAssistantUpdate != "updated" || conversation.TranscriptPath != "/tmp/conversation.jsonl" {
			t.Fatalf("conversation = %+v", conversation)
		}
		if usage := hookUsageMetadata("claude-code", payload); usage != nil {
			t.Fatalf("usage = %+v, want nil", usage)
		}
	})

	t.Run("malformed conversation retains usage", func(t *testing.T) {
		payload := []byte(`{"prompt":false,"transcript_path":"/tmp/usage.jsonl","model":"claude-sonnet","agent_id":"sub-1"}`)
		usage := hookUsageMetadata("claude-code", payload)
		if usage == nil || usage.TranscriptPath != "/tmp/usage.jsonl" || usage.ModelID != "claude-sonnet" || usage.SubagentID != "sub-1" {
			t.Fatalf("usage = %+v", usage)
		}
	})
}

func TestHookPayloadHelpersTolerateUTF8BOM(t *testing.T) {
	payload := append([]byte("\xef\xbb\xbf"), []byte(`{"session_id":"native-bom-1","tool_name":"Bash","tool_use_id":"toolu_1","launch_id":"launch-1","prompt":"do it","transcript_path":"/tmp/t.jsonl"}`)...)
	if got := hookAgentSessionID(payload); got != "native-bom-1" {
		t.Fatalf("hookAgentSessionID = %q, want native-bom-1", got)
	}
	if tool, useID := activityMeta(payload); tool != "Bash" || useID != "toolu_1" {
		t.Fatalf("activityMeta = (%q, %q), want (Bash, toolu_1)", tool, useID)
	}
	if got := hookLaunchID(payload); got != "launch-1" {
		t.Fatalf("hookLaunchID = %q, want launch-1", got)
	}
	facts := hookConversationFacts(domain.HarnessClaudeCode, "user-prompt-submit", payload)
	if facts.LatestUserPrompt != "do it" || facts.TranscriptPath != "/tmp/t.jsonl" {
		t.Fatalf("hookConversationFacts = %+v", facts)
	}
}

func TestCodexSubagentIDAttributesPreToolUse(t *testing.T) {
	if got := codexSubagentID("pre-tool-use", []byte(`{"tool_name":"Bash","agent_id":"child-1"}`)); got != "child-1" {
		t.Fatalf("pre-tool-use subagent id = %q, want child-1", got)
	}
	if got := codexSubagentID("pre-tool-use", []byte(`{"tool_name":"Bash"}`)); got != "" {
		t.Fatalf("main-thread pre-tool-use id = %q, want empty", got)
	}
}

func TestHookTurnHistoryFactsForwardsKiroText(t *testing.T) {
	for _, tt := range []struct {
		name, event, payload, request, response string
	}{
		{"prompt", "user-prompt-submit", `{"session_id":"s","prompt":"add a retry"}`, "add a retry", ""},
		{"early empty prompt", "user-prompt-submit", `{"session_id":"s","prompt":""}`, "", ""},
		{"stop with final message", "stop", `{"last_assistant_message":"retry added"}`, "", "retry added"},
		{"camel-case prompt", "user-prompt-submit", `{"userPrompt":"add a retry"}`, "add a retry", ""},
		{"Gemini-style response", "stop", `{"prompt":"add a retry","prompt_response":"retry added"}`, "", "retry added"},
		{"stop without one", "stop", `{"session_id":"s"}`, "", ""},
		{"Tallpine handoff note", "user-prompt-submit", `{"prompt":"<ao-handoff-request switch-id=\"x\">note</ao-handoff-request>"}`, "", ""},
		{"delivered worker report", "user-prompt-submit", `{"prompt":"<ao-report-delivery id=\"r-1\">\n[report] w1 done\n</ao-report-delivery>"}`, "[report] w1 done", ""},
		{"delivered session send", "user-prompt-submit", `{"prompt":"<ao-session-delivery id=\"d-1\">\nplease rebase\n</ao-session-delivery>"}`, "please rebase", ""},
		{"subagent delivery", "user-prompt-submit", `{"prompt":"<ao-session-delivery id=\"d-1\">\nx\n</ao-session-delivery>","agent_id":"child"}`, "", ""},
		{"subagent", "user-prompt-submit", `{"prompt":"child task","agent_id":"child"}`, "", ""},
		{"other event", "pre-tool-use", `{"prompt":"add a retry","last_assistant_message":"x"}`, "", ""},
		{"mistyped payload", "stop", `{"agent_id":7,"last_assistant_message":"child done"}`, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			facts := hookConversationFacts(domain.HarnessKiro, tt.event, []byte(tt.payload))
			request, response := facts.TurnUserRequest, facts.FinalAssistantMessage
			if request != tt.request || response != tt.response {
				t.Fatalf("got request %q response %q, want %q and %q", request, response, tt.request, tt.response)
			}
		})
	}
}

func mustJSONString(t *testing.T, value string) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
