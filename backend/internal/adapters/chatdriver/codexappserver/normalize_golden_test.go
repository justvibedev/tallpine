package codexappserver

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var updateNormalizeGolden = flag.Bool("update-normalize-golden", false, "rewrite testdata/normalize_golden.json")

// goldenNotifications covers every notification method normalizeNotification
// handles, plus the early-return branches (bad JSON, empty payload) and an
// unknown method. The golden file pins the events each produces, so the
// dispatch can be restructured without changing what any method emits.
var goldenNotifications = []struct{ name, method, params string }{
	{"turn_started", codexproto.MethodTurnStarted, `{"threadId":"th1","turn":{"id":"tu1","status":"inProgress","items":[]}}`},
	{"turn_started_bad_json", codexproto.MethodTurnStarted, `"not an object"`},
	{"turn_started_id_fallback", codexproto.MethodTurnStarted, `{"threadId":"th1","turnId":"tu9","turn":{"status":"inProgress"}}`},
	{"turn_completed", codexproto.MethodTurnCompleted, `{"threadId":"th1","turn":{"id":"tu1","status":"completed","items":[]}}`},
	{"turn_completed_failed", codexproto.MethodTurnCompleted, `{"threadId":"th1","turn":{"id":"tu1","status":"failed","items":[],"error":{"message":"Request failed","additionalDetails":"See https://example.com/help"}}}`},
	{"agent_message_delta", codexproto.MethodItemAgentMessageDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":"hello"}`},
	{"agent_message_delta_empty", codexproto.MethodItemAgentMessageDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":""}`},
	{"reasoning_summary_delta", codexproto.MethodItemReasoningSummaryTextDelta, `{"threadId":"th","turnId":"tu","itemId":"it","summaryIndex":0,"delta":"thinking"}`},
	{"reasoning_summary_delta_empty", codexproto.MethodItemReasoningSummaryTextDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":""}`},
	{"reasoning_part_added", codexproto.MethodItemReasoningSummaryPartAdded, `{"threadId":"th","turnId":"tu","itemId":"it","summaryIndex":1}`},
	{"reasoning_part_added_first", codexproto.MethodItemReasoningSummaryPartAdded, `{"threadId":"th","turnId":"tu","itemId":"it","summaryIndex":0}`},
	{"reasoning_text_delta", codexproto.MethodItemReasoningTextDelta, `{"threadId":"th","turnId":"tu","itemId":"it","contentIndex":0,"delta":"raw"}`},
	{"command_output_delta", codexproto.MethodItemCommandExecutionOutputDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":"tick-1\n"}`},
	{"command_output_delta_empty", codexproto.MethodItemCommandExecutionOutputDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":""}`},
	{"terminal_interaction", codexproto.MethodItemCommandExecutionTerminalInteraction, `{"threadId":"th","turnId":"tu","itemId":"it","processId":"123","stdin":"print(6*7)\n"}`},
	{"terminal_interaction_empty", codexproto.MethodItemCommandExecutionTerminalInteraction, `{"threadId":"th","turnId":"tu","itemId":"it","processId":"123","stdin":""}`},
	{"mcp_progress", codexproto.MethodItemMcpToolCallProgress, `{"threadId":"th","turnId":"tu","itemId":"it","message":"halfway"}`},
	{"mcp_progress_empty", codexproto.MethodItemMcpToolCallProgress, `{"threadId":"th","turnId":"tu","itemId":"it","message":""}`},
	{"file_change_output_delta", codexproto.MethodItemFileChangeOutputDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":"patched"}`},
	{"file_change_patch_updated", codexproto.MethodItemFileChangePatchUpdated, `{"threadId":"t","turnId":"u","itemId":"i","changes":[{"path":"a.txt","kind":{"type":"add"},"diff":"x\n"}]}`},
	{"file_change_patch_updated_empty", codexproto.MethodItemFileChangePatchUpdated, `{"threadId":"t","turnId":"u","itemId":"i","changes":[]}`},
	{"plan_delta", codexproto.MethodItemPlanDelta, `{"threadId":"th","turnId":"tu","itemId":"it","delta":"1. do"}`},
	{"turn_plan_updated", codexproto.MethodTurnPlanUpdated, `{"threadId":"th","turnId":"tu","explanation":"why","plan":[{"step":"a","status":"completed"},{"step":"b","status":"inProgress"},{"step":"c","status":"pending"}]}`},
	{"auto_review_started", codexproto.MethodItemAutoApprovalReviewStarted, `{"threadId":"th","turnId":"tu","reviewId":"r1","targetItemId":"it","startedAtMs":1000,"action":{"type":"command","command":"curl example.com"},"review":{"status":"inProgress"}}`},
	{"auto_review_completed", codexproto.MethodItemAutoApprovalReviewCompleted, `{"threadId":"th","turnId":"tu","reviewId":"r1","targetItemId":"it","startedAtMs":1000,"completedAtMs":2000,"decisionSource":"agent","action":{"type":"command","command":"curl example.com"},"review":{"status":"approved","riskLevel":"low","rationale":"safe"}}`},
	{"auto_review_denied", codexproto.MethodItemAutoApprovalReviewCompleted, `{"threadId":"th","turnId":"tu","reviewId":"r2","startedAtMs":1,"completedAtMs":2,"action":{"type":"command","command":"rm -rf /"},"review":{"status":"denied","riskLevel":"high"}}`},
	{"model_rerouted", codexproto.MethodModelRerouted, `{"threadId":"th","turnId":"tu","fromModel":"a","toModel":"b","reason":"highRiskCyberActivity"}`},
	{"model_rerouted_no_target", codexproto.MethodModelRerouted, `{"threadId":"th","turnId":"tu","fromModel":"a","toModel":""}`},
	{"account_updated", codexproto.MethodAccountUpdated, `{"authMode":"chatgpt","planType":"pro"}`},
	{"account_updated_empty", codexproto.MethodAccountUpdated, `{}`},
	{"thread_status_active", codexproto.MethodThreadStatusChanged, `{"threadId":"th","status":{"type":"active","activeFlags":["waitingOnApproval"]}}`},
	{"thread_status_idle", codexproto.MethodThreadStatusChanged, `{"threadId":"th","status":{"type":"idle"}}`},
	{"thread_status_unknown", codexproto.MethodThreadStatusChanged, `{"threadId":"th","status":{"type":"somethingNew"}}`},
	{"thread_archived", codexproto.MethodThreadArchived, `{"threadId":"th"}`},
	{"thread_unarchived", codexproto.MethodThreadUnarchived, `{"threadId":"th"}`},
	{"thread_closed", codexproto.MethodThreadClosed, `{"threadId":"th"}`},
	{"mcp_startup_status", codexproto.MethodMcpServerStartupStatusUpdated, `{"name":"probe","status":"failed","error":"boom"}`},
	{"mcp_startup_status_nameless", codexproto.MethodMcpServerStartupStatusUpdated, `{"name":"","status":"ready"}`},
	{"turn_diff_updated", codexproto.MethodTurnDiffUpdated, `{"threadId":"t","turnId":"u","diff":"diff --git a/a.txt b/a.txt\nnew file mode 100644\n--- /dev/null\n+++ b/a.txt\n@@ -0,0 +1 @@\n+hi\n"}`},
	{"turn_diff_updated_empty", codexproto.MethodTurnDiffUpdated, `{"threadId":"t","turnId":"u","diff":""}`},
	{"item_started_command", codexproto.MethodItemStarted, `{"turnId":"tu","item":{"id":"c1","type":"commandExecution","command":"ls -la","cwd":"/tmp","status":"inProgress"}}`},
	{"item_started_agent_message", codexproto.MethodItemStarted, `{"turnId":"tu","item":{"id":"msg_1","type":"agentMessage","text":""}}`},
	{"item_completed_command", codexproto.MethodItemCompleted, `{"turnId":"tu","item":{"id":"c1","type":"commandExecution","command":"ls","cwd":"/tmp","status":"completed","aggregatedOutput":"a\n","exitCode":0}}`},
	{"item_completed_reasoning", codexproto.MethodItemCompleted, `{"turnId":"tu","item":{"id":"r1","type":"reasoning","summary":["one","two"]}}`},
	{"item_completed_error", codexproto.MethodItemCompleted, `{"turnId":"t","item":{"id":"i","type":"error","message":"boom"}}`},
	{"item_completed_mcp_error", codexproto.MethodItemCompleted, `{"threadId":"th","turnId":"tu","item":{"type":"mcpToolCall","id":"i","server":"s","tool":"t","error":{"message":"tool exploded"}}}`},
	{"item_completed_unknown_type", codexproto.MethodItemCompleted, `{"turnId":"t","item":{"id":"i","type":"someFutureItem"}}`},
	{"token_usage_updated", codexproto.MethodThreadTokenUsageUpdated, `{"threadId":"th","turnId":"tu","tokenUsage":{"total":{"totalTokens":18055,"inputTokens":18050,"cachedInputTokens":11008,"outputTokens":5},"last":{"totalTokens":9000},"modelContextWindow":258400}}`},
	{"rate_limits_updated", codexproto.MethodAccountRateLimitsUpdated, `{"rateLimits":{"limitId":"codex","primary":{"usedPercent":71,"windowDurationMins":10080,"resetsAt":1786159947},"secondary":null,"planType":"pro"}}`},
	{"thread_compacted", codexproto.MethodThreadCompacted, `{"threadId":"th1","turnId":"t1"}`}, //nolint:staticcheck // deliberately reading the deprecated spelling
	{"thread_name_updated", codexproto.MethodThreadNameUpdated, `{"threadId":"th","threadName":"  Fix the build  "}`},
	{"thread_name_cleared", codexproto.MethodThreadNameUpdated, `{"threadId":"th"}`},
	{"server_request_resolved_string", codexproto.MethodServerRequestResolved, `{"threadId":"th1","requestId":"r-42"}`},
	{"server_request_resolved_number", codexproto.MethodServerRequestResolved, `{"requestId":2}`},
	{"server_request_resolved_empty", codexproto.MethodServerRequestResolved, `{}`},
	{"error_plain", codexproto.MethodError, `{"threadId":"th1","turnId":"tu1","willRetry":false,"error":{"message":"Request failed","additionalDetails":"See https://example.com/help"}}`},
	{"error_will_retry", codexproto.MethodError, `{"threadId":"th1","turnId":"tu1","willRetry":true,"error":{"message":"Stream disconnected"}}`},
	{"error_unparseable", codexproto.MethodError, `"just text"`},
	{"unknown_method", "thread/somethingNew", `{"threadId":"th"}`},
}

func TestNormalizeNotificationGolden(t *testing.T) {
	type entry struct {
		Name   string            `json:"name"`
		Method string            `json:"method"`
		Events []json.RawMessage `json:"events"`
	}
	var got []entry
	for _, tc := range goldenNotifications {
		events := normalizeNotification(notification{Method: tc.method, Params: json.RawMessage(tc.params)}, time.Unix(1785669503, 0))
		e := entry{Name: tc.name, Method: tc.method, Events: []json.RawMessage{}}
		for _, ev := range events {
			e.Events = append(e.Events, goldenEvent(t, ev))
		}
		got = append(got, e)
	}
	data, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "normalize_golden.json")
	if *updateNormalizeGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != string(data)+"\n" {
		t.Fatalf("normalizeNotification output drifted from %s; rerun with -update-normalize-golden if intended", path)
	}
}

// goldenEvent renders an event with its error (an interface) as text.
func goldenEvent(t *testing.T, ev ports.ChatEvent) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if ev.Err != nil {
		m["Err"] = fmt.Sprintf("%T: %v", ev.Err, ev.Err)
	}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestNormalizeGoldenCoversEveryHandler fails when a method is added to
// notificationHandlers without a golden case.
func TestNormalizeGoldenCoversEveryHandler(t *testing.T) {
	covered := map[string]bool{}
	for _, tc := range goldenNotifications {
		covered[tc.method] = true
	}
	for method := range notificationHandlers {
		if !covered[method] {
			t.Errorf("notificationHandlers[%q] has no case in goldenNotifications", method)
		}
	}
}
