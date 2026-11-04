package acp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestACPDriverParksAndResolvesStructuredElicitation(t *testing.T) {
	request := acpsdk.NewUnstableCreateElicitationRequestForm(acpsdk.UnstableElicitationSchema{
		Type:       acpsdk.UnstableElicitationSchemaTypeObject,
		Properties: map[string]any{"choice": map[string]any{"type": "string"}},
		Required:   []string{"choice"},
	})
	request.Form.Message = "Which approach?"
	agent := &fakeAgent{elicitation: &request}
	driver := newFakeDriver(agent)

	conv := startConversation(t, driver)
	_ = nextEvent(t, conv.Events())
	ref, err := conv.SendTurn(context.Background(), ports.ChatUserMessage{Text: "ask me"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conv.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}

	var requestID string
	for requestID == "" {
		event := nextEvent(t, conv.Events())
		if event.Kind == ports.ChatEventInputRequested {
			requestID = event.RequestID
			if event.Input == nil || event.Input.Mode != "form" || event.Input.Message != "Which approach?" {
				t.Fatalf("input request = %#v", event.Input)
			}
		}
	}
	if err := conv.(ports.ChatInputResponder).ResolveInput(context.Background(), requestID,
		ports.ChatInputResponse{Action: "accept", Content: map[string]any{"choice": "native"}}); err != nil {
		t.Fatalf("ResolveInput: %v", err)
	}
	for {
		if event := nextEvent(t, conv.Events()); event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	agent.mu.Lock()
	response := agent.elicitationResponse
	agent.mu.Unlock()
	if response.Accept == nil || response.Accept.Content["choice"] != "native" {
		t.Fatalf("elicitation response = %#v", response)
	}
}

func TestValidateInputResponseRejectsValuesOutsideTheProviderSchema(t *testing.T) {
	request := ports.ChatInputRequest{Mode: "form", Schema: map[string]any{
		"required": []any{"choice", "fast"},
		"properties": map[string]any{
			"choice": map[string]any{"type": "string", "oneOf": []any{
				map[string]any{"const": "native"}, map[string]any{"const": "bridge"},
			}},
			"fast": map[string]any{"type": "boolean"},
		},
	}}
	for name, response := range map[string]ports.ChatInputResponse{
		"missing required": {Action: "accept", Content: map[string]any{"choice": "native"}},
		"unknown option":   {Action: "accept", Content: map[string]any{"choice": "other", "fast": true}},
		"wrong type":       {Action: "accept", Content: map[string]any{"choice": "native", "fast": "yes"}},
		"unknown field":    {Action: "accept", Content: map[string]any{"choice": "native", "fast": true, "secret": "x"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInputResponse(request, response); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
				t.Fatalf("error = %v, want ErrChatDecisionNotOffered", err)
			}
		})
	}
}

func TestValidateInputResponseAcceptsAMarkedOtherAnswerForARequiredQuestion(t *testing.T) {
	question := map[string]any{"type": "string", "oneOf": []any{
		map[string]any{"const": "Native"}, map[string]any{"const": "Bridge"},
	}}
	// The field shape claude-agent-acp's askUserQuestionsToCreateRequest sends.
	other := map[string]any{"type": "string", "title": "Other", "_meta": map[string]any{
		askUserQuestionCustomAnswerMetaKey: map[string]any{"questionId": "question_0", "isCustomAnswer": true},
	}}
	request := ports.ChatInputRequest{Mode: "form", Schema: map[string]any{
		"required":   []any{"question_0"},
		"properties": map[string]any{"question_0": question, "question_0_custom": other},
	}}
	for name, content := range map[string]map[string]any{
		"choice":       {"question_0": "Native"},
		"other answer": {"question_0_custom": "Hybrid"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateInputResponse(request, ports.ChatInputResponse{Action: "accept", Content: content}); err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
	if err := validateInputResponse(request, ports.ChatInputResponse{
		Action: "accept", Content: map[string]any{"question_0_custom": "  "},
	}); !errors.Is(err, ports.ErrChatDecisionNotOffered) {
		t.Fatalf("blank other answer: error = %v, want ErrChatDecisionNotOffered", err)
	}
}

func TestValidateInputResponseKeepsUnmarkedCustomFieldsIndependent(t *testing.T) {
	// A generic MCP form may have a "name_custom" (or even "question_0_custom")
	// field of its own; without the marker it never answers the required field.
	for _, field := range []string{"name", "question_0"} {
		t.Run(field, func(t *testing.T) {
			request := ports.ChatInputRequest{Mode: "form", Schema: map[string]any{
				"required": []any{field},
				"properties": map[string]any{
					field:             map[string]any{"type": "string"},
					field + "_custom": map[string]any{"type": "string"},
				},
			}}
			err := validateInputResponse(request, ports.ChatInputResponse{
				Action: "accept", Content: map[string]any{field + "_custom": "Alias"},
			})
			if !errors.Is(err, ports.ErrChatDecisionNotOffered) {
				t.Fatalf("error = %v, want ErrChatDecisionNotOffered", err)
			}
		})
	}
}

func TestACPDriverPreservesNestedToolAndTerminalMetadata(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)
	opened := startActiveTurn(t, driver)

	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: "child-tool", Title: "Run tests",
			Kind: acpsdk.ToolKindExecute, Status: acpsdk.ToolCallStatusPending,
			Meta: map[string]any{
				"claudeCode":    map[string]any{"toolName": "Bash", "parentToolUseId": "agent-tool"},
				"terminal_info": map[string]any{"terminal_id": "child-tool"},
			},
		}},
	}); err != nil {
		t.Fatalf("tool start: %v", err)
	}
	started := nextEvent(t, opened.Events())
	if started.Kind != ports.ChatEventActivityStarted {
		t.Fatalf("started event = %#v", started)
	}

	status := acpsdk.ToolCallStatusCompleted
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
			SessionUpdate: "tool_call_update", ToolCallId: "child-tool", Status: &status,
			Meta: map[string]any{
				"terminal_output": map[string]any{"terminal_id": "child-tool", "data": "ok\n"},
				"terminal_exit":   map[string]any{"terminal_id": "child-tool", "exit_code": float64(0)},
			},
		}},
	}); err != nil {
		t.Fatalf("tool completion: %v", err)
	}
	output := nextEvent(t, opened.Events())
	completed := nextEvent(t, opened.Events())
	if output.Kind != ports.ChatEventCommandOutputDelta || output.Delta != "ok\n" {
		t.Fatalf("terminal output = %#v", output)
	}
	var detail map[string]any
	if err := json.Unmarshal(completed.Detail, &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail["parentProviderItemId"] != "agent-tool" || detail["terminalId"] != "child-tool" || detail["output"] != "ok\n" {
		t.Fatalf("tool detail = %#v", detail)
	}
}

// Only a binding whose agent keeps one list for the session marks its plan as
// spanning turns; every other ACP plan stays a plan for the turn it arrived in.
func TestACPDriverMarksPlansThatSpanTurns(t *testing.T) {
	for _, spans := range []bool{false, true} {
		t.Run(fmt.Sprintf("PlansSpanTurns=%v", spans), func(t *testing.T) {
			agent := &fakeAgent{}
			cfg := fakeConfig()
			cfg.Capabilities = ports.ChatCapabilities{ports.ChatCapabilityPlans: true}
			cfg.PlansSpanTurns = spans
			opened := startActiveTurn(t, newDriverWith(cfg, agent))

			if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
				Update: acpsdk.SessionUpdate{Plan: &acpsdk.SessionUpdatePlan{
					SessionUpdate: "plan",
					Entries: []acpsdk.PlanEntry{
						{Content: "one", Status: acpsdk.PlanEntryStatusCompleted},
						{Content: "two", Status: acpsdk.PlanEntryStatusPending},
					},
				}},
			}); err != nil {
				t.Fatalf("plan update: %v", err)
			}
			event := nextEvent(t, opened.Events())
			if event.Kind != ports.ChatEventPlanUpdated || event.Plan == nil {
				t.Fatalf("plan event = %#v", event)
			}
			if event.Plan.SpansTurns != spans {
				t.Fatalf("plan SpansTurns = %v, want %v", event.Plan.SpansTurns, spans)
			}
			if len(event.Plan.Steps) != 2 || event.Plan.Steps[1].Status != domain.PlanStepPending {
				t.Fatalf("plan steps = %#v", event.Plan.Steps)
			}
		})
	}
}

func TestACPDriverExtractsCommandFromExecuteToolInput(t *testing.T) {
	agent := &fakeAgent{}
	driver := newFakeDriver(agent)
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()

	// claude-code's Bash tool reports rawInput as {"command": "..."} — exactly
	// the shape the neutral `detail.command` contract must be filled from.
	rawInput := map[string]any{"command": "/bin/zsh -lc 'tallpine session ls'"}
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: "bash-1", Title: "List sessions",
			Kind: acpsdk.ToolKindExecute, Status: acpsdk.ToolCallStatusPending,
			RawInput: rawInput,
		}},
	}); err != nil {
		t.Fatalf("tool start: %v", err)
	}
	started := nextEvent(t, opened.Events())
	if started.Kind != ports.ChatEventActivityStarted {
		t.Fatalf("started event = %#v", started)
	}
	var detail map[string]any
	if err := json.Unmarshal(started.Detail, &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail["command"] != "tallpine session ls" {
		t.Fatalf("detail.command = %#v, want unwrapped %q", detail["command"], "tallpine session ls")
	}
	if detail["rawCommand"] != "/bin/zsh -lc 'tallpine session ls'" {
		t.Fatalf("detail.rawCommand = %#v, want the verbatim provider command", detail["rawCommand"])
	}
	if detail["input"] == nil {
		t.Fatalf("detail.input dropped: %#v", detail)
	}
}

// A tool row's detail is rewritten on every update and carried by every
// snapshot, so an html_render page and an html_preview screenshot are not
// stored whole.
func TestACPToolDetailCapsInputAndDropsImageData(t *testing.T) {
	page := map[string]any{"html": strings.Repeat("<p>x</p>", 2000), "title": "Turns"}
	shot := strings.Repeat("iVBORw0KGgo", 400)
	tool := &toolState{
		id: "mcp-1", kind: acpsdk.ToolKindOther, status: acpsdk.ToolCallStatusCompleted, rawInput: page,
		content: []acpsdk.ToolCallContent{
			acpsdk.ToolContent(acpsdk.ImageBlock(shot, "image/png")),
			acpsdk.ToolContent(acpsdk.TextBlock(`{"width":720}`)),
		},
		rawOutput: []any{
			map[string]any{"type": "image", "data": shot, "mimeType": "image/png"},
			map[string]any{"type": "text", "text": `{"width":720}`},
		},
	}
	event := (&conversation{}).toolEvent("turn-1", tool, true)
	approval := approvalToolDetail(acpsdk.ToolCallUpdate{RawInput: page}, domain.ActivityKindMCPTool, "")

	for name, raw := range map[string][]byte{"tool": event.Detail, "approval": approval} {
		if strings.Contains(string(raw), `\u003cp\u003ex\u003c/p\u003e`) || strings.Contains(string(raw), shot) {
			t.Fatalf("%s detail stored the page or the screenshot: %.300s", name, raw)
		}
		var detail struct {
			Input map[string]any `json:"input"`
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			t.Fatal(err)
		}
		if detail.Input["truncated"] != true {
			t.Fatalf("%s input = %v, want the truncation marker", name, detail.Input)
		}
	}
	var detail struct {
		Content []json.RawMessage `json:"content"`
		Output  string            `json:"output"`
	}
	if err := json.Unmarshal(event.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	stub := fmt.Sprintf(`{"bytes":%d,"mimeType":"image/png","type":"image"}`, len(shot))
	if len(detail.Content) != 2 || string(detail.Content[0]) != `{"content":`+stub+`,"type":"content"}` ||
		string(detail.Content[1]) != `{"content":{"text":"{\"width\":720}","type":"text"},"type":"content"}` {
		t.Fatalf("content = %s", detail.Content)
	}
	if want := `[` + stub + `,{"text":"{\"width\":720}","type":"text"}]`; detail.Output != want {
		t.Fatalf("output = %s\nwant     %s", detail.Output, want)
	}
}

// claude-agent-acp passes an MCP image result through as rawOutput in the
// Anthropic shape, with the base64 under source.data.
func TestACPToolDetailDropsAnthropicImageData(t *testing.T) {
	shot := strings.Repeat("iVBORw0KGgo", 400)
	tool := &toolState{
		id: "mcp-2", kind: acpsdk.ToolKindOther, status: acpsdk.ToolCallStatusCompleted,
		rawOutput: []any{
			map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": shot}},
			map[string]any{"type": "text", "text": "ok"},
		},
	}
	event := (&conversation{}).toolEvent("turn-1", tool, true)
	if strings.Contains(string(event.Detail), shot) {
		t.Fatalf("detail stored the screenshot: %.300s", event.Detail)
	}
	var detail struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(event.Detail, &detail); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`[{"bytes":%d,"mimeType":"image/png","type":"image"},{"text":"ok","type":"text"}]`, len(shot))
	if detail.Output != want {
		t.Fatalf("output = %s\nwant     %s", detail.Output, want)
	}
}

func TestACPToolDetailKeepsReadContentAndSmallInput(t *testing.T) {
	file := strings.Repeat("line of a file\n", 2000)
	tool := &toolState{
		id: "read-1", kind: acpsdk.ToolKindRead, status: acpsdk.ToolCallStatusCompleted,
		rawInput: map[string]any{"file_path": "/repo/main.go"},
		content:  []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock(file))},
	}
	var detail struct {
		Input   map[string]any           `json:"input"`
		Content []acpsdk.ToolCallContent `json:"content"`
	}
	if err := json.Unmarshal((&conversation{}).toolEvent("turn-1", tool, true).Detail, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Input["file_path"] != "/repo/main.go" || len(detail.Content) != 1 ||
		detail.Content[0].Content == nil || detail.Content[0].Content.Content.Text == nil ||
		detail.Content[0].Content.Content.Text.Text != file {
		t.Fatalf("read detail changed: input=%v content=%+v", detail.Input, detail.Content)
	}
}

func TestRawCommandFromInput(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{name: "nil", raw: nil, want: ""},
		{name: "string passthrough is not an object", raw: "go test ./...", want: ""},
		{
			name: "claude-code bash",
			raw:  map[string]any{"command": "rg -n pattern src/", "description": "search"},
			want: "rg -n pattern src/",
		},
		{
			name: "shell-wrapped command",
			raw:  map[string]any{"command": "/bin/bash -c 'go build ./...'"},
			want: "/bin/bash -c 'go build ./...'",
		},
		{name: "empty command", raw: map[string]any{"command": "  "}, want: ""},
		{name: "cmd key", raw: map[string]any{"cmd": "ls"}, want: "ls"},
		{
			name: "edit tool input has no command",
			raw:  map[string]any{"file_path": "/tmp/x", "old": "a", "new": "b"},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rawCommandFromInput(tt.raw); got != tt.want {
				t.Fatalf("rawCommandFromInput(%v) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestToolEventPreservesCommandExitCode(t *testing.T) {
	intPtr := func(value int) *int { return &value }
	for _, tt := range []struct {
		name         string
		raw          string
		kind         acpsdk.ToolKind
		status       acpsdk.ToolCallStatus
		terminalExit *int
		wantExit     *int
		wantStatus   domain.ActivityStatus
	}{
		{"nonzero shell exit", `{"output":"command output\n","metadata":{"exit":7}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, intPtr(7), domain.ActivityStatusFailed},
		{"successful shell exit", `{"output":"command output\n","metadata":{"exit":0}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, intPtr(0), domain.ActivityStatusCompleted},
		{"missing exit", `{"output":"command output\n","metadata":{}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, nil, domain.ActivityStatusCompleted},
		{"null exit", `{"output":"command output\n","metadata":{"exit":null}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, nil, domain.ActivityStatusCompleted},
		{"string exit", `{"output":"command output\n","metadata":{"exit":"7"}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, nil, domain.ActivityStatusCompleted},
		{"fractional exit", `{"output":"command output\n","metadata":{"exit":1.5}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, nil, nil, domain.ActivityStatusCompleted},
		{"non-command metadata", `{"output":"command output\n","metadata":{"exit":7}}`, acpsdk.ToolKindRead, acpsdk.ToolCallStatusCompleted, nil, nil, domain.ActivityStatusCompleted},
		{"provider failure stays failed", `{"output":"command output\n","metadata":{"exit":0}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusFailed, nil, intPtr(0), domain.ActivityStatusFailed},
		{"terminal exit takes precedence", `{"output":"command output\n","metadata":{"exit":7}}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, intPtr(0), intPtr(0), domain.ActivityStatusCompleted},
		{"terminal nonzero exit", `{"output":"command output\n"}`, acpsdk.ToolKindExecute, acpsdk.ToolCallStatusCompleted, intPtr(7), intPtr(7), domain.ActivityStatusFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var raw any
			if err := json.Unmarshal([]byte(tt.raw), &raw); err != nil {
				t.Fatal(err)
			}
			tool := &toolState{id: "command-1", kind: tt.kind, status: tt.status, rawOutput: raw, rawInput: map[string]any{"command": "printf test; exit 7"}}
			if tt.terminalExit != nil {
				tool.meta = map[string]any{"terminal_exit": map[string]any{"exit_code": *tt.terminalExit}}
			}
			conv := &conversation{}
			event := conv.toolEvent("turn-1", tool, true)
			if event.Kind != ports.ChatEventActivityCompleted || event.ActivityStatus != tt.wantStatus {
				t.Fatalf("event kind/status = %s/%s, want completed/%s", event.Kind, event.ActivityStatus, tt.wantStatus)
			}
			var detail map[string]any
			if err := json.Unmarshal(event.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			if tt.wantExit == nil {
				if _, exists := detail["exitCode"]; exists {
					t.Fatalf("unexpected exitCode: %#v", detail["exitCode"])
				}
			} else if detail["exitCode"] != float64(*tt.wantExit) {
				t.Fatalf("exitCode = %#v, want %d", detail["exitCode"], *tt.wantExit)
			}
			if detail["output"] != "command output\n" {
				t.Fatalf("output changed: %#v", detail["output"])
			}
		})
	}
}

func TestToolOutputTextNormalizesProviderDefinedRawOutput(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want string
	}{
		{name: "plain text", raw: "ok\n", want: "ok\n"},
		{
			name: "OpenCode output envelope",
			raw: map[string]any{
				"metadata": map[string]any{"exit": float64(0), "output": "metadata copy"},
				"output":   "command output\n",
			},
			want: "command output\n",
		},
		{
			name: "error envelope",
			raw:  map[string]any{"error": "Tool execution aborted"},
			want: "Tool execution aborted",
		},
		{
			name: "unknown structured output remains visible",
			raw:  map[string]any{"result": true},
			want: `{"result":true}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toolOutputText(tt.raw); got != tt.want {
				t.Fatalf("toolOutputText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestACPDriverMapsCostRateLimitsAndAuthRecovery(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := New(Config{
		Harness:         domain.HarnessClaudeCode,
		Capabilities:    ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:           func(context.Context) error { return nil },
		Launch:          func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		UsageRateLimits: stubUsageRateLimits,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())

	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{UsageUpdate: &acpsdk.SessionUsageUpdate{
			SessionUpdate: "usage_update", Used: 25, Size: 100, Cost: &acpsdk.Cost{Amount: 1.25, Currency: "USD"},
			Meta: map[string]any{"_test/rateLimit": true},
		}},
	}); err != nil {
		t.Fatalf("usage update: %v", err)
	}
	usageEvent := nextEvent(t, opened.Events())
	limitEvent := nextEvent(t, opened.Events())
	if usageEvent.Usage == nil || usageEvent.Usage.Cost == nil || *usageEvent.Usage.Cost != 1.25 || usageEvent.Usage.Currency != "USD" {
		t.Fatalf("usage event = %#v", usageEvent)
	}
	if limitEvent.RateLimits == nil || limitEvent.RateLimits.PrimaryUsedPercent != 80 || limitEvent.RateLimits.PrimaryResetsInSeconds < 3500 {
		t.Fatalf("rate-limit event = %#v", limitEvent)
	}

	agent.mu.Lock()
	agent.promptNoPermission = false
	agent.promptErr = acpsdk.NewAuthRequired(nil)
	agent.mu.Unlock()
	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "continue"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	for {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventAccountChanged || event.Kind == ports.ChatEventError {
			t.Fatalf("terminal auth failure emitted a second event: %#v", event)
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			if event.TurnState != domain.TurnStateFailed {
				t.Fatalf("turn state = %q", event.TurnState)
			}
			if !errors.Is(event.Err, ports.ErrChatAuthRequired) {
				t.Fatalf("completion error = %#v", event.Err)
			}
			break
		}
	}
}

func TestACPDriverNormalizesClaudeRetryStatus(t *testing.T) {
	agent := &fakeAgent{
		promptBlock:   true,
		promptStarted: make(chan struct{}, 1),
	}
	driver := newFakeDriver(agent)
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	agent.mu.Lock()
	clientMeta := agent.initParams.ClientCapabilities.Meta
	agent.mu.Unlock()
	jetbrains, _ := clientMeta["jetbrains"].(map[string]any)
	air, _ := jetbrains["air"].(map[string]any)
	version, versionOK := number(air["version"])
	capabilities, _ := air["capabilities"].([]any)
	capability := ""
	if len(capabilities) == 2 {
		capability, _ = capabilities[0].(string)
	}
	if !versionOK || version != 1 || capability != "sessionFailure" || capabilities[1] != "asyncTasks" {
		t.Fatalf("session failure capability = %#v", air)
	}

	ref, err := opened.SendTurn(context.Background(), ports.ChatUserMessage{Text: "continue"})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if err := opened.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	select {
	case <-agent.promptStarted:
	case <-time.After(time.Second):
		t.Fatal("ACP prompt did not start")
	}

	sendSessionFailure(t, agent, opened.ProviderConversationID(), map[string]any{
		"id":       "claude-turn:error",
		"revision": float64(2),
		"category": "connection",
		"severity": "warning",
		"title":    "Reconnecting to Claude, attempt 2 of 10.",
		"details":  "The API request failed. Trying again in 4s.",
		"actions":  []any{"new_session"},
	})

	var retry ports.ChatEvent
	for retry.Kind == "" {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventActivityStarted && strings.HasPrefix(event.ProviderItemID, "session-failure:") {
			retry = event
		}
	}
	if retry.ProviderTurnID != ref.ProviderTurnID ||
		retry.ActivityKind != domain.ActivityKindSystem ||
		retry.ActivityStatus != domain.ActivityStatusRunning ||
		retry.Summary != "Reconnecting to Claude, attempt 2 of 10." {
		t.Fatalf("retry event = %#v", retry)
	}
	var detail map[string]any
	if err := json.Unmarshal(retry.Detail, &detail); err != nil {
		t.Fatalf("retry detail: %v", err)
	}
	if detail["event"] != "provider.failure" ||
		detail["category"] != "connection" ||
		detail["severity"] != "warning" ||
		detail["text"] != "The API request failed. Trying again in 4s." {
		t.Fatalf("retry detail = %#v", detail)
	}

	// Claude can use a new extension incident id for each attempt before its
	// provider turn id is available. Tallpine must still update one active-episode row.
	sendSessionFailure(t, agent, opened.ProviderConversationID(), map[string]any{
		"id":       "another-incident-id",
		"revision": float64(1),
		"category": "connection",
		"severity": "warning",
		"title":    "Reconnecting to Claude, attempt 3 of 10.",
		"details":  "Connection error. Trying again in 8s.",
	})
	nextRetry := nextEvent(t, opened.Events())
	if nextRetry.Kind != ports.ChatEventActivityStarted ||
		nextRetry.ProviderItemID != retry.ProviderItemID ||
		nextRetry.Summary != "Reconnecting to Claude, attempt 3 of 10." {
		t.Fatalf("next retry event = %#v", nextRetry)
	}

	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
			SessionUpdate: "agent_message_chunk",
			Content:       acpsdk.ContentBlock{Text: &acpsdk.ContentBlockText{Text: "Back online."}},
		}},
	}); err != nil {
		t.Fatalf("recovery update: %v", err)
	}
	recovered := nextEvent(t, opened.Events())
	if recovered.Kind != ports.ChatEventActivityCompleted ||
		recovered.ProviderItemID != retry.ProviderItemID ||
		recovered.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("recovered retry event = %#v", recovered)
	}
}
