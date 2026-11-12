package claudeacp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
	acpdriver "tallpine.eu/tallpine/backend/internal/adapters/chatdriver/acp"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestClaudePromptResponseFailureRecordsVersionTooOld(t *testing.T) {
	claudecode.ConfigureModelVersionRequirements("")
	t.Cleanup(func() { claudecode.ConfigureModelVersionRequirements("") })
	response := acpsdk.PromptResponse{
		StopReason: acpsdk.StopReasonEndTurn,
		Meta: map[string]any{"jetbrains": map[string]any{"air": map[string]any{
			"version": float64(1),
			"sessionFailure": map[string]any{
				"id": "bad-request", "severity": "error", "category": "bad_request",
				"title": `API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"Claude Code 2.1.239 does not support this model; version 2.1.280 or newer is required. Run 'claude update', or update the Claude desktop app, then try again.","details":{"error_code":"claude_code_version_too_old"}}}`,
			},
		}}},
	}

	err := claudePromptResponseFailure(response, "us.anthropic.claude-opus-5-5-v1:0")
	if !errors.Is(err, ports.ErrAgentVersionTooOld) {
		t.Fatalf("error = %v, want ErrAgentVersionTooOld", err)
	}
	if !strings.Contains(err.Error(), "2.1.280") || !strings.Contains(err.Error(), "claude update") || strings.Contains(err.Error(), "API Error") {
		t.Fatalf("error text = %q, want the remedy instead of the raw API error", err.Error())
	}
	if got, ok := claudecode.DefaultModelVersionRequirements().Required("us.anthropic.claude-opus-5-5-v1:0"); !ok || got != "2.1.280" {
		t.Fatalf("recorded requirement = %q, %v", got, ok)
	}

	if err := claudePromptResponseFailure(response, "opus"); !errors.Is(err, ports.ErrAgentVersionTooOld) {
		t.Fatalf("alias error = %v, want ErrAgentVersionTooOld", err)
	}
	if _, ok := claudecode.DefaultModelVersionRequirements().Required("opus"); ok {
		t.Fatal("recorded a version requirement for an alias")
	}
}

func TestClaudeServedModelOnlyReportsAliases(t *testing.T) {
	configDir := t.TempDir()
	sessionID := "8320947f-5806-4579-943f-b23a943bac1f"
	projectDir := filepath.Join(configDir, "projects", "-work")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	turnStart := time.Now().Add(-time.Minute)
	transcript := `{"type":"assistant","timestamp":"` + time.Now().UTC().Format(time.RFC3339Nano) +
		`","message":{"model":"claude-opus-4-8"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := acpdriver.LaunchConfig{Env: map[string]string{"CLAUDE_CONFIG_DIR": configDir}}

	served, reason := claudeServedModel(context.Background(), cfg, sessionID, "opus", turnStart)
	if served != "claude-opus-4-8" || !strings.Contains(reason, "opus") {
		t.Fatalf("served = %q, reason = %q", served, reason)
	}
	if served, _ := claudeServedModel(context.Background(), cfg, sessionID, "claude-opus-4-8", turnStart); served != "" {
		t.Fatalf("concrete model reported as rerouted to %q", served)
	}
}

// A turn that wrote no assistant message (native /clear, a local command) after
// a switch from Opus to an alias must not report the previous turn's model as
// what the alias resolves to.
func TestClaudeServedModelIgnoresMessagesBeforeTheTurn(t *testing.T) {
	configDir := t.TempDir()
	sessionID := "8320947f-5806-4579-943f-b23a943bac1f"
	projectDir := filepath.Join(configDir, "projects", "-work")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	earlier := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	transcript := `{"type":"assistant","timestamp":"` + earlier + `","message":{"model":"claude-opus-5-5"}}` + "\n"
	if err := os.WriteFile(filepath.Join(projectDir, sessionID+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := acpdriver.LaunchConfig{Env: map[string]string{"CLAUDE_CONFIG_DIR": configDir}}
	if served, _ := claudeServedModel(context.Background(), cfg, sessionID, "fable", time.Now()); served != "" {
		t.Fatalf("stale message reported as served model %q", served)
	}
}
