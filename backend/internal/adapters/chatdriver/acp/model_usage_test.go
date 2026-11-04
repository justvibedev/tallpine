package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// The payload is the shape claude-agent-acp 0.99.0 builds in quotaMeta
// (dist/acp-agent.js:9634): the `_meta.quota.model_usage` rows are this turn's
// increment by model.
const claudeQuotaResponse = `{"stopReason":"end_turn",
 "usage":{"inputTokens":12,"outputTokens":40,"totalTokens":3252,"cachedReadTokens":3000,"cachedWriteTokens":200},
 "_meta":{"quota":{
   "token_count":{"totalTokens":3252,"inputTokens":12,"cachedInputTokens":3000,"cachedWriteTokens":200,"outputTokens":40,"reasoningOutputTokens":0},
   "model_usage":[
     {"model":"claude-sonnet-5-5","token_count":{"totalTokens":3252,"inputTokens":12,"cachedInputTokens":3000,"cachedWriteTokens":200,"outputTokens":40,"reasoningOutputTokens":0}},
     {"model":"claude-haiku-5-5","token_count":{"totalTokens":0,"inputTokens":0,"cachedInputTokens":0,"cachedWriteTokens":0,"outputTokens":0,"reasoningOutputTokens":0}},
     {"model":"claude-haiku-5-5-b","token_count":{"totalTokens":9,"inputTokens":4,"cachedInputTokens":0,"cachedWriteTokens":0,"outputTokens":5,"reasoningOutputTokens":0}}
   ]}}}`

func TestQuotaModelUsageParsesClaudeAgentACPPayload(t *testing.T) {
	var resp acpsdk.PromptResponse
	if err := json.Unmarshal([]byte(claudeQuotaResponse), &resp); err != nil {
		t.Fatal(err)
	}
	got := quotaModelUsage(resp.Meta)
	want := []domain.ModelUsage{
		{Model: "claude-sonnet-5-5", InputTokens: 12, CacheReadTokens: 3000, CacheCreationTokens: 200, OutputTokens: 40},
		{Model: "claude-haiku-5-5-b", InputTokens: 4, OutputTokens: 5},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if quotaModelUsage(nil) != nil {
		t.Fatal("no meta must yield no usage")
	}
}

// The real prompt path: a finished turn's response meta reaches a usage event.
func TestPromptResponseModelUsageReachesUsageEvent(t *testing.T) {
	var resp acpsdk.PromptResponse
	if err := json.Unmarshal([]byte(claudeQuotaResponse), &resp); err != nil {
		t.Fatal(err)
	}
	agent := &fakeAgent{promptResponse: &resp}
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityUsage: true},
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		TurnUsage: func(meta map[string]any, model string) []domain.ModelUsage {
			return []domain.ModelUsage{{Model: "hook-" + model, Credits: 1, Turns: 1}}
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened := startHelloTurn(t, driver)
	var models []domain.ModelUsage
	for {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventUsage && event.Usage != nil {
			models = append(models, event.Usage.ModelUsage...)
		}
		if event.Kind == ports.ChatEventTurnCompleted {
			break
		}
	}
	if len(models) != 3 || models[0].Model != "claude-sonnet-5-5" || models[0].CacheReadTokens != 3000 || models[2].Credits != 1 || models[2].Turns != 1 {
		t.Fatalf("model usage = %+v", models)
	}
}
