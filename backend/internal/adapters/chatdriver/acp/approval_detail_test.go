package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// requestApprovalEvent parks one permission request and returns the approval
// event the driver emitted for it.
func requestApprovalEvent(t *testing.T, prime func(*conversation), tool acpsdk.ToolCallUpdate) ports.ChatEvent {
	t.Helper()
	return requestApprovalEventMeta(t, prime, tool, nil)
}

func requestApprovalEventMeta(t *testing.T, prime func(*conversation), tool acpsdk.ToolCallUpdate, meta map[string]any) ports.ChatEvent {
	t.Helper()
	driver := New(Config{
		Harness:      domain.HarnessClaudeCode,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityApprovals: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(&fakeAgent{}))
	opened := startConversation(t, driver)
	nextEvent(t, opened.Events())
	conv := opened.(*conversation)
	if prime != nil {
		prime(conv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, _ = conv.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
			ToolCall: tool, Meta: meta,
			Options: []acpsdk.PermissionOption{
				{OptionId: "allow-once", Name: "Allow once", Kind: acpsdk.PermissionOptionKindAllowOnce},
			},
		})
	}()
	for {
		event := nextEvent(t, opened.Events())
		if event.Kind == ports.ChatEventApprovalRequested {
			return event
		}
	}
}

func approvalDetail(t *testing.T, event ports.ChatEvent) map[string]any {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal(event.Detail, &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	return detail
}

// A bare request (what dsh sends) is described by the earlier tool_call update.
func TestACPPermissionRequestCarriesCommandAndReasonFromAnnouncedToolCall(t *testing.T) {
	event := requestApprovalEvent(t, func(c *conversation) {
		c.mu.Lock()
		c.tools["call-1"] = &toolState{
			id: "call-1", title: "bash", kind: acpsdk.ToolKindExecute,
			rawInput: map[string]any{"command": "git commit -m x", "reason": "escalate sandbox: committing writes the worktree index"},
		}
		c.mu.Unlock()
	}, acpsdk.ToolCallUpdate{ToolCallId: "call-1"})
	detail := approvalDetail(t, event)
	if event.Summary != "bash" || detail["rawCommand"] != "git commit -m x" ||
		detail["reason"] != "escalate sandbox: committing writes the worktree index" {
		t.Fatalf("summary %q detail %v", event.Summary, detail)
	}
}

func TestACPPermissionRequestWithoutToolDetailKeepsGenericText(t *testing.T) {
	event := requestApprovalEvent(t, nil, acpsdk.ToolCallUpdate{ToolCallId: "unknown"})
	detail := approvalDetail(t, event)
	if event.Summary != "Permission required" || detail["reason"] != nil || detail["rawCommand"] != nil {
		t.Fatalf("summary %q detail %v", event.Summary, detail)
	}
}

func TestApprovalDetailTruncationCountsBytesOnRuneBoundary(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", 5000) // 10000 bytes, 5000 runes: under a rune cap, over a byte cap
	got := truncateBytes(long, maxApprovalCommandBytes)
	if len(got) > maxApprovalCommandBytes || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("len %d valid %v: truncated command must fit %d bytes on a rune boundary", len(got), utf8.ValidString(got), maxApprovalCommandBytes)
	}
	if short := "ls -la"; truncateBytes(short, maxApprovalReasonBytes) != short {
		t.Fatal("short text must pass through")
	}
}

// A bypass-immune Claude request carries its decisionReason as
// jetbrains.air.permission.description; it must reach the stored approval so a
// bypass-mode session's prompt is explainable.
func TestApprovalCarriesProviderDecisionReason(t *testing.T) {
	title := "Bash"
	meta := map[string]any{"jetbrains": map[string]any{"air": map[string]any{"permission": map[string]any{
		"version": 1, "title": "ls", "description": "Reason: contains zsh expansion flags",
	}}}}
	event := requestApprovalEventMeta(t, nil, acpsdk.ToolCallUpdate{Title: &title, RawInput: map[string]any{"command": "ls"}}, meta)
	if got := approvalDetail(t, event)["reason"]; got != "contains zsh expansion flags" {
		t.Fatalf("reason = %v, want the provider decisionReason", got)
	}
}
