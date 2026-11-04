package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func compactTool(status string, raw any) *toolState {
	return &toolState{
		id: "c1", status: acpsdk.ToolCallStatus(status), rawOutput: raw,
		meta: map[string]any{"claudeCode": map[string]any{"toolName": "compact"}},
	}
}

func TestCompactionFromToolReadsLifecycle(t *testing.T) {
	t.Parallel()
	facts := func(trigger string) map[string]any {
		return map[string]any{"trigger": trigger, "preTokens": float64(410000), "postTokens": float64(62000)}
	}
	tests := []struct {
		name string
		tool *toolState
		want *ports.ChatCompaction
	}{
		{"start", compactTool("in_progress", nil), &ports.ChatCompaction{ID: "c1", Status: domain.CompactionRunning}},
		{"finish automatic", compactTool("completed", facts("automatic")),
			&ports.ChatCompaction{ID: "c1", Status: domain.CompactionCompleted, Trigger: "auto", PreTokens: 410000, PostTokens: 62000}},
		{"finish manual", compactTool("completed", facts("manual")),
			&ports.ChatCompaction{ID: "c1", Status: domain.CompactionCompleted, Trigger: "manual", PreTokens: 410000, PostTokens: 62000}},
		{"failed", compactTool("failed", nil), &ports.ChatCompaction{ID: "c1", Status: domain.CompactionFailed}},
		{"other tool is not compaction", &toolState{id: "x", status: "completed",
			meta: map[string]any{"claudeCode": map[string]any{"toolName": "Bash"}}}, nil},
		{"no meta", &toolState{id: "y", status: "completed"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := compactionFromTool(tt.tool)
			if (tt.want != nil) != ok {
				t.Fatalf("ok = %v", ok)
			}
			if tt.want != nil && *got != *tt.want {
				t.Fatalf("got %+v, want %+v", *got, *tt.want)
			}
		})
	}
}

// The adapter reports compaction as a "compact" tool call. Finishing it must
// report the lifecycle and move the context position to the post-compaction size.
func TestACPDriverCompactionLifecycleUpdatesContext(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := newFakeDriver(agent)
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	sid := acpsdk.SessionId(opened.ProviderConversationID())
	send := func(update acpsdk.SessionUpdate) {
		t.Helper()
		if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{SessionId: sid, Update: update}); err != nil {
			t.Fatalf("update: %v", err)
		}
	}
	meta := map[string]any{"claudeCode": map[string]any{"toolName": "compact"}}

	send(acpsdk.SessionUpdate{UsageUpdate: &acpsdk.SessionUsageUpdate{SessionUpdate: "usage_update", Used: 410000, Size: 1000000}})
	_ = nextEvent(t, opened.Events())

	send(acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: "c1", Title: "Compact conversation",
		Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusInProgress, Meta: meta,
	}})
	started := nextOfKind(t, opened.Events(), ports.ChatEventThreadState)
	if started.ThreadState.Compaction == nil || started.ThreadState.Compaction.Status != domain.CompactionRunning {
		t.Fatalf("start = %+v", started.ThreadState)
	}

	status := acpsdk.ToolCallStatusCompleted
	send(acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: "c1", Status: &status, Meta: meta,
		RawOutput: map[string]any{"trigger": "automatic", "preTokens": float64(410000), "postTokens": float64(62000)},
	}})
	finished := nextOfKind(t, opened.Events(), ports.ChatEventThreadState)
	if c := finished.ThreadState.Compaction; c == nil || c.Status != domain.CompactionCompleted || c.Trigger != "auto" ||
		c.PreTokens != 410000 || c.PostTokens != 62000 {
		t.Fatalf("finish = %+v", finished.ThreadState)
	}
	usage := nextOfKind(t, opened.Events(), ports.ChatEventUsage)
	if usage.Usage.ContextUsed != 62000 || usage.Usage.ContextWindow != 1000000 || !usage.Usage.ContextKnown {
		t.Fatalf("usage after finish = %+v", usage.Usage)
	}
}

func nextOfKind(t *testing.T, events <-chan ports.ChatEvent, kind ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	for range 20 {
		if event := nextEvent(t, events); event.Kind == kind {
			return event
		}
	}
	t.Fatalf("no %s event", kind)
	return ports.ChatEvent{}
}

// A cancelled compaction gets no terminal report from the adapter. Settling the
// turn must not leave the thread reporting it as running.
func TestSettlingTurnEndsOpenCompaction(t *testing.T) {
	agent := &fakeAgent{promptNoPermission: true}
	driver := newFakeDriver(agent)
	opened := startConversation(t, driver)
	_ = nextEvent(t, opened.Events())
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(opened.ProviderConversationID()),
		Update: acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: "c1", Title: "Compact conversation",
			Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusInProgress,
			Meta: map[string]any{"claudeCode": map[string]any{"toolName": "compact"}},
		}},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := nextOfKind(t, opened.Events(), ports.ChatEventThreadState); got.ThreadState.Compaction.Status != domain.CompactionRunning {
		t.Fatalf("start = %+v", got.ThreadState.Compaction)
	}
	opened.(*conversation).settleOpenItems("")
	if got := nextOfKind(t, opened.Events(), ports.ChatEventThreadState); got.ThreadState.Compaction.Status != domain.CompactionFailed {
		t.Fatalf("after settle = %+v", got.ThreadState.Compaction)
	}
}

// The adapter heartbeats an open compaction; a repeat must not be reported again.
func TestRepeatedCompactionReportIsDropped(t *testing.T) {
	t.Parallel()
	c := &conversation{}
	tool := compactTool("in_progress", nil)
	var kinds []ports.ChatEventKind
	emit := func(e ports.ChatEvent) { kinds = append(kinds, e.Kind) }
	c.emitCompaction(tool, "", emit)
	c.emitCompaction(tool, "", emit)
	if len(kinds) != 1 {
		t.Fatalf("emitted %v, want one report", kinds)
	}
}
