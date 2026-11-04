package cli

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/adapters/agent/activitydispatch"
	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/pricing"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

type activityCapture struct {
	body string
	path string
	hits int
}

func TestClaudeSubmissionContextMatchesDurableNonce(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	previous := ""
	for i := 0; i < 2; i++ {
		stdout, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native","prompt_id":"same-running-prompt","prompt":"continue"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", "claude-code", "user-prompt-submit")
		if err != nil {
			t.Fatal(err)
		}
		var request apitypes.SetActivityRequest
		var output sessionStartHookOutput
		if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(stdout), &output); err != nil {
			t.Fatal(err)
		}
		if _, err := uuid.Parse(request.SubmissionID); err != nil {
			t.Fatal(err)
		}
		if request.SubmissionID == previous || output.HookSpecificOutput.HookEventName != "UserPromptSubmit" ||
			output.HookSpecificOutput.AdditionalContext != domain.NativeSubmissionContext(request.SubmissionID) {
			t.Fatalf("submission identity mismatch: request=%+v output=%+v", request, output)
		}
		previous = request.SubmissionID
	}
}

func TestClaudeHookReportsNativeEntrypoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct{ agent, env, want string }{
		{"claude-code", "sdk-cli", "sdk-cli"},
		{"claude-code", "cli", "cli"},
		{"claude-code", "", ""},
		// Only Claude defines this marker; other harnesses never forward it.
		{"codex", "sdk-cli", ""},
	} {
		t.Setenv("CLAUDE_CODE_ENTRYPOINT", tc.env)
		hits := capture.hits
		if _, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native-side"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", tc.agent, "session-end"); err != nil {
			t.Fatal(err)
		}
		if capture.hits != hits+1 {
			t.Fatalf("%s session-end posted %d activity requests, want 1", tc.agent, capture.hits-hits)
		}
		var request apitypes.SetActivityRequest
		if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
			t.Fatal(err)
		}
		if request.Entrypoint != tc.want {
			t.Fatalf("%s entrypoint %q forwarded as %q, want %q", tc.agent, tc.env, request.Entrypoint, tc.want)
		}
	}
}

func TestClaudeHookReportsItsProcess(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct {
		agent, env string
		want       int
	}{
		{"claude-code", "4242", 4242},
		{"claude-code", "", 0},
		{"claude-code", "not-a-pid", 0},
		{"claude-code", "1", 0},
		// Only Claude names the process running a hook.
		{"codex", "4242", 0},
	} {
		t.Setenv("CLAUDE_PID", tc.env)
		if _, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native-1"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", tc.agent, "stop"); err != nil {
			t.Fatal(err)
		}
		var request apitypes.SetActivityRequest
		if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
			t.Fatal(err)
		}
		if request.ProcessID != tc.want {
			t.Fatalf("%s CLAUDE_PID %q forwarded as %d, want %d", tc.agent, tc.env, request.ProcessID, tc.want)
		}
	}
}

func TestClaudeSubmissionRetryReusesContextNonce(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "qa-1")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	var requests []apitypes.SetActivityRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request apitypes.SetActivityRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests = append(requests, request)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"code":"ACTIVITY_PROJECTION_BUSY","message":"retry","requestId":"nonce-retry"}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true}`)
		}
	}))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	stdout, _, err := executeCLI(t, Deps{
		In: strings.NewReader(`{"session_id":"native","prompt":"continue"}`), ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "user-prompt-submit")
	if err != nil {
		t.Fatal(err)
	}
	var output sessionStartHookOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].SubmissionID == "" || requests[0].SubmissionID != requests[1].SubmissionID ||
		output.HookSpecificOutput.AdditionalContext != domain.NativeSubmissionContext(requests[0].SubmissionID) {
		t.Fatalf("retry changed submission identity: %+v", requests)
	}
}

// activityServer accepts POST /api/v1/sessions/{id}/activity and records what
// the CLI sent. It mirrors sendServer in send_test.go.
func activityServer(t *testing.T, status int, respBody string) (*httptest.Server, *activityCapture) {
	t.Helper()
	capture := &activityCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/activity") {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		capture.body = string(body)
		capture.path = r.URL.Path
		capture.hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

func assertActivityRequest(t *testing.T, got, want apitypes.SetActivityRequest) {
	t.Helper()
	if got.ObservedAt.IsZero() {
		t.Fatal("hook omitted its observation time")
	}
	// The exact timestamp has a separate deterministic wire-contract test.
	got.ObservedAt = time.Time{}
	// Every hook names the agent that sent it; the value has its own test.
	if got.Harness == "" {
		t.Fatal("hook omitted the agent that sent it")
	}
	got.Harness = ""
	if got != want {
		t.Fatalf("body = %+v, want %+v", got, want)
	}
}

func capturedState(t *testing.T, capture *activityCapture) string {
	t.Helper()
	var req struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	return req.State
}

func TestHooks_ReportsUsageTranscriptMetadata(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("ANTHROPIC_BASE_URL", "")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "")
	t.Setenv("CLAUDE_CODE_USE_VERTEX", "")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"sessionId":"tp-7","state":""}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In: strings.NewReader(`{
			"session_id":"native-7",
			"transcript_path":"/home/user/.claude/projects/p/native-7.jsonl",
			"model":"claude-sonnet",
			"agent_id":"sub-2",
			"agent_transcript_path":"/home/user/.claude/projects/p/agent-sub-2.jsonl",
			"cli_version":"9.4.1"
		}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "subagent-stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if req.AgentSessionID != "native-7" || req.Usage == nil {
		t.Fatalf("request = %+v", req)
	}
	if req.Usage.Harness != "claude-code" ||
		req.Usage.ProviderID != "anthropic" ||
		req.Usage.TranscriptPath != "/home/user/.claude/projects/p/native-7.jsonl" ||
		req.Usage.SubagentID != "sub-2" ||
		req.Usage.SubagentTranscriptPath != "/home/user/.claude/projects/p/agent-sub-2.jsonl" {
		t.Fatalf("usage metadata = %+v", req.Usage)
	}
	if strings.Contains(capture.body, "sourceCliVersion") {
		t.Fatalf("usage request retained obsolete CLI version metadata: %s", capture.body)
	}
}

func TestClaudeHookUsageProviderHintUsesTrustedProcessRouting(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		bedrock string
		vertex  string
		foundry string
		want    string
	}{
		{name: "default anthropic", want: "anthropic"},
		{name: "official anthropic api", baseURL: "https://api.anthropic.com/v1/messages", want: "anthropic"},
		{name: "official zai api", baseURL: "https://api.z.ai/api/anthropic", want: "zai"},
		{name: "bedrock precedence", baseURL: "https://custom.invalid", bedrock: "1", want: "bedrock"},
		{name: "vertex precedence", baseURL: "https://api.z.ai", vertex: "true", want: "vertex_ai"},
		// A route Tallpine cannot name is still a route. Reporting silence would make
		// it indistinguishable from "no hook has run", which is what lets the
		// legacy repairer fall back to the model that answered — and that
		// fallback would bill a proxied session at Anthropic list rates.
		{name: "conflicting flags", bedrock: "1", vertex: "1", want: pricing.UnidentifiedBillingRoute},
		{name: "unknown custom route", baseURL: "https://token:secret@custom.invalid/v1",
			want: pricing.UnidentifiedBillingRoute},
		{name: "foundry only", want: pricing.UnidentifiedBillingRoute, foundry: "1"},
		{name: "unparseable base url", baseURL: "://nonsense", want: pricing.UnidentifiedBillingRoute},
		{name: "non http scheme", baseURL: "ftp://example.com", want: pricing.UnidentifiedBillingRoute},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("ANTHROPIC_BASE_URL", test.baseURL)
			t.Setenv("CLAUDE_CODE_USE_BEDROCK", test.bedrock)
			t.Setenv("CLAUDE_CODE_USE_VERTEX", test.vertex)
			t.Setenv("CLAUDE_CODE_USE_FOUNDRY", test.foundry)
			t.Setenv("ANTHROPIC_API_KEY", "credential-must-not-persist")
			got := activitydispatch.Facts("claude-code", "session-start", []byte(`{"transcript_path":"/tmp/transcript.jsonl"}`)).Usage
			stampClaudeRoute("claude-code", got)
			if got == nil || got.ProviderID != test.want {
				t.Fatalf("usage metadata = %+v, want provider %q", got, test.want)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if test.baseURL != "" && strings.Contains(string(encoded), test.baseURL) ||
				strings.Contains(string(encoded), "credential-must-not-persist") || strings.Contains(string(encoded), "secret") {
				t.Fatalf("usage metadata persisted routing secret or URL: %s", encoded)
			}
		})
	}

	t.Setenv("ANTHROPIC_BASE_URL", "https://api.z.ai")
	codexUsage := activitydispatch.Facts("codex", "session-start", []byte(`{"transcript_path":"/tmp/rollout.jsonl"}`)).Usage
	stampClaudeRoute("codex", codexUsage)
	if got := codexUsage; got == nil || got.ProviderID != "" {
		t.Fatalf("Codex inherited Claude routing hint: %+v", got)
	}
}

func capturedAgentSessionID(t *testing.T, capture *activityCapture) string {
	t.Helper()
	var req struct {
		AgentSessionID string `json:"agentSessionId"`
	}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	return req.AgentSessionID
}

func TestHooks_ReviewerRoutesToReviewActivity(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
	t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-7")
	t.Setenv("TALLPINE_REVIEW_HARNESS", "codex")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"reviewSessionId":"review-7"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"codex-native-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/reviews/review-7/activity" {
		t.Fatalf("path = %q, want /api/v1/reviews/review-7/activity", capture.path)
	}
	if got := capturedAgentSessionID(t, capture); got != "codex-native-1" {
		t.Fatalf("agentSessionId = %q, want codex-native-1", got)
	}
}

func TestHooks_ReviewerActivityOmitsToolCorrelationFields(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
	t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-7")
	t.Setenv("TALLPINE_REVIEW_HARNESS", "claude-code")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"reviewSessionId":"review-7"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash","tool_use_id":"toolu_42","tool_response":"ok"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "post-tool-use")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/reviews/review-7/activity" {
		t.Fatalf("path = %q, want /api/v1/reviews/review-7/activity", capture.path)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	if _, ok := req["toolName"]; ok {
		t.Fatalf("reviewer activity included toolName: body=%s", capture.body)
	}
	if _, ok := req["toolUseId"]; ok {
		t.Fatalf("reviewer activity included toolUseId: body=%s", capture.body)
	}
}

func TestHooks_ReviewerRoutingTakesPrecedenceOverWorkerSession(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
	t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-context-only")
	t.Setenv("TALLPINE_SESSION_ID", "worker-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"codex-native-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/reviews/review-7/activity" {
		t.Fatalf("path = %q, want reviewer route", capture.path)
	}
}

func TestHooks_ReviewWorkerSessionIDDoesNotRouteWithoutReviewSessionID(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-context-only")
	t.Setenv("TALLPINE_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"codex-native-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.hits != 0 {
		t.Fatalf("review worker context routed unexpectedly: path=%q body=%s", capture.path, capture.body)
	}
}

func TestHooks_NotificationReportsBlocked(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"sessionId":"tp-7","state":"blocked"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"permission_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/tp-7/activity" {
		t.Errorf("path = %q, want /api/v1/sessions/tp-7/activity", capture.path)
	}
	if got := capturedState(t, capture); got != "blocked" {
		t.Errorf("state = %q, want blocked", got)
	}
}

func TestHooks_IdlePromptReportsIdle(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"sessionId":"tp-7","state":"idle"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"idle_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if got := capturedState(t, capture); got != "idle" {
		t.Errorf("state = %q, want idle (idle_prompt is not a blocking request)", got)
	}
}

func TestHooks_DottedProjectSessionIDReportsActivity(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "acme.dev-2")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/sessions/acme.dev-2/activity" {
		t.Fatalf("activity path = %q, want the dotted session's activity endpoint", capture.path)
	}
}

func TestHooks_SessionEndReportsExited(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "exited" {
		t.Errorf("state = %q, want exited", got)
	}
}

func TestHooks_ThreadsRuntimeLaunchID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LaunchID != "launch-3" {
		t.Fatalf("launch id = %q, want launch-3", req.LaunchID)
	}
}

func TestHooks_PayloadLaunchIDFallbackWhenEnvUnset(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"launch_id":"launch-from-payload"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "permission-request")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LaunchID != "launch-from-payload" {
		t.Fatalf("launch id = %q, want launch-from-payload", req.LaunchID)
	}
	if got := capturedState(t, capture); got != "blocked" {
		t.Errorf("state = %q, want blocked", got)
	}
}

func TestHooks_StopReportsIdle(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "idle" {
		t.Errorf("state = %q, want idle", got)
	}
}

// Claude fires StopFailure instead of Stop when an API error (usage limit,
// unknown model, expired login) ends the turn. It must report idle, tagged so
// lifecycle treats it as a turn boundary, and must not certify the error text
// as the turn's assistant answer.
func TestHooks_ClaudeStopFailureReportsIdle(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"session_id":"native-1","hook_event_name":"StopFailure","error":"rate_limit","error_details":"429 usage limit","last_assistant_message":"API Error: usage limit reached"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop-failure")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.State != "idle" || req.Event != "stop-failure" || req.AgentSessionID != "native-1" {
		t.Fatalf("request = %#v, want idle stop-failure for native-1", req)
	}
	if req.LatestAssistantUpdate != "" || req.LatestUserPrompt != "" {
		t.Fatalf("stop-failure reported conversation facts: %#v", req)
	}
}

func TestHooks_ClaudeStopCarriesRunningSubagents(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	payload := `{"session_id":"native-main","background_tasks":[{"id":"child-1","type":"subagent","status":"running"},{"id":"shell-1","type":"bash","status":"running"}]}`
	_, _, err := executeCLI(t, Deps{
		In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.State != "idle" || req.RunningSubagentIDs == nil || len(*req.RunningSubagentIDs) != 1 || (*req.RunningSubagentIDs)[0] != "child-1" {
		t.Fatalf("Stop request = %+v", req)
	}
	_, _, err = executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-main","background_tasks":[]}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	req = apitypes.SetActivityRequest{}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.RunningSubagentIDs == nil || len(*req.RunningSubagentIDs) != 0 {
		t.Fatalf("empty background snapshot lost: %+v", req)
	}
	_, _, err = executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-main"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	req = apitypes.SetActivityRequest{}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.RunningSubagentIDs != nil {
		t.Fatalf("unavailable task registry became an empty snapshot: %+v", req)
	}
	_, _, err = executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-main","agent_id":"child-1","background_tasks":[{"id":"child-2","type":"subagent"}]}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "subagent-stop")
	if err != nil {
		t.Fatal(err)
	}
	req = apitypes.SetActivityRequest{}
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.SubagentID != "child-1" || req.RunningSubagentIDs == nil || len(*req.RunningSubagentIDs) != 1 || (*req.RunningSubagentIDs)[0] != "child-2" {
		t.Fatalf("SubagentStop parent snapshot = %+v", req)
	}
}

func TestHooks_ClaudeSubagentIdentityDoesNotBecomeMainConversation(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	for _, event := range []string{"subagent-start", "pre-tool-use", "subagent-stop"} {
		_, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native-main","agent_id":"child-1","tool_name":"Bash","last_assistant_message":"child answer"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", "claude-code", event)
		if err != nil {
			t.Fatal(err)
		}
		var req apitypes.SetActivityRequest
		if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
			t.Fatal(err)
		}
		if req.SubagentID != "child-1" || req.AgentSessionID != "native-main" || req.LatestAssistantUpdate != "" {
			t.Fatalf("%s request = %+v", event, req)
		}
	}
}

func TestHooks_CodexSubagentEventsCarryChildIdentity(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	for _, event := range []string{"subagent-start", "user-prompt-submit", "subagent-stop"} {
		_, _, err := executeCLI(t, Deps{
			In:           strings.NewReader(`{"session_id":"native-root","agent_id":"child-1","last_assistant_message":"child answer"}`),
			ProcessAlive: func(int) bool { return true },
		}, "hooks", "codex", event)
		if err != nil {
			t.Fatal(err)
		}
		var req apitypes.SetActivityRequest
		if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
			t.Fatal(err)
		}
		if req.Event != event || req.SubagentID != "child-1" || req.AgentSessionID != "native-root" || req.LaunchID != "launch-3" ||
			req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "" || req.ProviderTurnID != "" {
			t.Fatalf("%s request = %+v", event, req)
		}
	}
}

func TestHooks_CodexSpawnToolResultCarriesProvisionalChild(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	payload := `{"session_id":"native-root","tool_name":"spawn_agent","tool_use_id":"call-1","tool_response":"{\"task_name\":\"/root/worker\"}"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "post-tool-use")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Event != "subagent-spawn" || req.SubagentID != "call-1" || req.AgentSessionID != "native-root" || req.LaunchID != "launch-3" || req.State != "" {
		t.Fatalf("spawn request = %+v", req)
	}
	for _, payload := range []string{
		`{"tool_name":"spawn_agent","tool_use_id":"call-1","tool_response":"failed"}`,
		`{"tool_name":"spawn_agent","tool_use_id":"call-1","tool_response":"{\"task_name\":\"/root/worker\"}","agent_id":"child-1"}`,
		`{"tool_name":"collaborationspawn_agent","tool_use_id":"call-1","tool_response":"failed"}`,
		`{"tool_name":"collaborationspawn_agent","tool_use_id":"call-1","tool_response":"{\"task_name\":\"/root/worker\"}","agent_id":"child-1"}`,
		`{"tool_name":"exec_command","tool_use_id":"call-1","tool_response":"{\"task_name\":\"/root/worker\"}"}`,
	} {
		if got := activitydispatch.Facts("codex", "post-tool-use", []byte(payload)).SubagentID; got != "" {
			t.Fatalf("non-root successful spawn parsed as %q from %s", got, payload)
		}
	}
}

func TestHooks_StopReportsOnlyMainAssistantCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"prompt":"finish the regression test","last_assistant_message":"I updated the generation fence.","transcript_path":"/tmp/provider/session.jsonl"}`
	observedAt := time.Date(2026, 9, 13, 0, 0, 0, 0, time.UTC)
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		Now:          func() time.Time { return observedAt },
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "I updated the generation fence." {
		t.Fatalf("conversation facts = %#v", req)
	}
	if req.TranscriptPath != "/tmp/provider/session.jsonl" {
		t.Fatalf("transcript path = %q", req.TranscriptPath)
	}
	var wire struct {
		ObservedAt time.Time `json:"observedAt"`
	}
	if err := json.Unmarshal([]byte(capture.body), &wire); err != nil || !wire.ObservedAt.Equal(observedAt) {
		t.Fatalf("hook lost its pre-delivery observation time: %v err=%v", wire.ObservedAt, err)
	}
}

func TestHooks_CodexStopDoesNotReportClaudeAssistantCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"last_assistant_message":"Claude-only checkpoint field","transcript_path":"/tmp/provider/session.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestAssistantUpdate != "" {
		t.Fatalf("Codex Stop promoted Claude-only checkpoint = %q", req.LatestAssistantUpdate)
	}
}

func TestHooks_UserPromptSubmitReportsOnlyMainUserCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"prompt":"finish the regression test","last_assistant_message":"stale answer from the prior turn","transcript_path":"/tmp/provider/session.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "user-prompt-submit")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "finish the regression test" || req.LatestAssistantUpdate != "" ||
		req.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginHuman {
		t.Fatalf("conversation facts = %#v", req)
	}
}

func TestHooks_SubagentStopCannotReportMainConversationCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"session_id":"native-main","agent_id":"subagent-1","prompt":"subagent task","last_assistant_message":"subagent answer","transcript_path":"/tmp/provider/session.jsonl","agent_transcript_path":"/tmp/provider/subagent.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "subagent-stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "" {
		t.Fatalf("subagent conversation facts escaped onto the main checkpoint: %#v", req)
	}
}

func TestHooks_InternalHandoffStopCannotReportAssistantCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"session_id":"native-main","prompt":"<ao-handoff-request switch-id=\"switch-1\">prepare context","last_assistant_message":"internal handoff submitted","transcript_path":"/tmp/provider/session.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "" {
		t.Fatalf("internal handoff escaped onto the main checkpoint: %#v", req)
	}
}

func TestHooks_InternalContinuationStopCannotReportConversationCheckpoint(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-3")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	prompt := "Tallpine transferred the previous agent's context in hidden system instructions. Continue a clear, safe, already-authorized unfinished action; otherwise, acknowledge the current objective and wait for the user."
	payload := `{"session_id":"native-main","prompt":` + mustJSONString(t, prompt) + `,"last_assistant_message":"Tallpine continuation acknowledged","transcript_path":"/tmp/provider/session.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "" ||
		req.ConversationCheckpointOrigin != domain.ConversationCheckpointOriginCoordination {
		t.Fatalf("internal continuation escaped onto the completed checkpoint: %#v", req)
	}
}

func TestHooks_NonSwitchingHarnessDoesNotReportConversationFacts(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	payload := `{"prompt":"private kiro prompt","last_assistant_message":"private kiro response","transcript_path":"/tmp/kiro/session.jsonl"}`
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(payload),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "kiro", "stop")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.LatestUserPrompt != "" || req.LatestAssistantUpdate != "" || req.TranscriptPath != "" {
		t.Fatalf("non-switching harness reported conversation facts: %#v", req)
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

func TestHooks_SessionStartReportsNativeSessionIDWithoutActivity(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"019f6af0-codex-session"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{Event: "session-start", AgentSessionID: "019f6af0-codex-session"}
	assertActivityRequest(t, req, want)
}

func TestHooks_ActivityAlsoReportsNativeSessionID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"claude-session-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "stop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{State: "idle", Event: "stop", AgentSessionID: "claude-session-1"}
	assertActivityRequest(t, req, want)
}

func TestHooks_UnknownAgentCannotReportNativeSessionID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"untrusted-session"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "unknown-agent", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Fatalf("unknown agent reported metadata; hits=%d body=%s", capture.hits, capture.body)
	}
}

func TestHooks_ClaudeCodePermissionRequestReportsBlocked(t *testing.T) {
	// claude-code installs the pre/post-tool-use trio, so a permission-request
	// blocked state can be correlated and cleared — it is the one harness that
	// reports blocked.
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "permission-request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := capturedState(t, capture); got != "blocked" {
		t.Errorf("state = %q, want blocked", got)
	}
}

func TestHooks_PostToolUseCarriesCorrelationFields(t *testing.T) {
	// Tool-use signals must carry the event and the native tool identity so
	// lifecycle can clear a stale blocked only on the approved tool's post.
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash","tool_use_id":"toolu_42","tool_response":"ok"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "post-tool-use")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{State: "active", Event: "post-tool-use", ToolName: "Bash", ToolUseID: "toolu_42"}
	assertActivityRequest(t, req, want)
}

func TestHooks_EventWithoutToolIdentityOmitsIt(t *testing.T) {
	// Adapters whose payloads carry no tool fields (codex permission-request
	// payload here has tool_name only) still tag the event; missing identity
	// fields stay empty rather than inventing values.
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"tool_name":"Bash"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "permission-request")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{State: "waiting_input", Event: "permission-request", ToolName: "Bash", ToolUseID: ""}
	assertActivityRequest(t, req, want)
}

func TestHooks_CodexSessionStartReportsAgentSessionID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"codex-native-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 1 {
		t.Fatalf("daemon calls = %d, want 1", capture.hits)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{Event: "session-start", AgentSessionID: "codex-native-1"}
	assertActivityRequest(t, req, want)
}

func TestHooks_CodexBlankSessionIDIsIgnored(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"   "}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "codex", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Fatalf("daemon calls = %d, want 0", capture.hits)
	}
}

func TestHooks_ClaudeCodeSessionStartReportsAgentSessionID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"claude-native-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 1 {
		t.Fatalf("daemon calls = %d, want 1", capture.hits)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
	}
	want := apitypes.SetActivityRequest{Event: "session-start", AgentSessionID: "claude-native-1"}
	assertActivityRequest(t, req, want)
}

func TestHooks_ClaudeCodeBlankSessionIDIsIgnored(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"   "}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-start")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Fatalf("daemon calls = %d, want 0", capture.hits)
	}
}

func TestHooks_RegisteredHarnessSessionStartReportsAgentSessionID(t *testing.T) {
	sessionStartState := map[string]string{
		"kiro": "active",
	}
	for agent, wantState := range sessionStartState {
		t.Run(agent, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"session_id":"` + agent + `-native-1"}`),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", agent, "session-start")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capture.hits != 1 {
				t.Fatalf("daemon calls = %d, want 1", capture.hits)
			}
			var req apitypes.SetActivityRequest
			if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
				t.Fatalf("decode body: %v\nbody=%s", err, capture.body)
			}
			want := apitypes.SetActivityRequest{State: wantState, Event: "session-start", AgentSessionID: agent + "-native-1"}
			assertActivityRequest(t, req, want)
		})
	}
}

func TestHooks_RejectsMalformedSessionID(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "../etc/passwd")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for an out-of-alphabet session id, got %d", capture.hits)
	}
}

func TestHooks_NoSessionIDIsNoOp(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"idle_prompt"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for a non-Tallpine session, got %d", capture.hits)
	}
}

func TestHooks_UntrackedEventIsNoOp(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"notification_type":"auth_success"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "notification")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capture.hits != 0 {
		t.Errorf("expected no daemon call for an untracked notification, got %d", capture.hits)
	}
}

func TestHooks_DaemonDownIsBestEffort(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	setConfigEnv(t) // no run-file written: daemon is "not running"

	_, _, err := executeCLI(t, Deps{
		In: strings.NewReader(`{"reason":"logout"}`),
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must be best-effort (exit 0) when the daemon is down, got: %v", err)
	}
}

// TestHooks_DeliveryFailureGoesToHooksLog covers the durable failure sink:
// agents swallow hook stderr, so a delivery failure must also land in
// $TALLPINE_DATA_DIR/hooks.log — and a delivered hook must not write the file at all.
func TestHooks_DeliveryFailureGoesToHooksLog(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantLog bool
		wantIn  []string
	}{
		{
			name:    "daemon error is appended",
			status:  http.StatusInternalServerError,
			body:    `{"error":"internal","code":"BOOM","message":"boom"}`,
			wantLog: true,
			wantIn:  []string{"tallpine hooks claude-code session-end", "session=tp-7"},
		},
		{
			name:   "successful delivery writes nothing",
			status: http.StatusOK,
			body:   `{"ok":true}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			srv, _ := activityServer(t, tc.status, tc.body)
			writeRunFileFor(t, cfg, srv)

			_, _, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"reason":"logout"}`),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", "claude-code", "session-end")
			if err != nil {
				t.Fatalf("hooks must exit 0, got: %v", err)
			}

			logPath := filepath.Join(cfg.dataDir, "hooks.log")
			data, err := os.ReadFile(logPath)
			if !tc.wantLog {
				if !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("hooks.log should not exist after a delivered hook, got err=%v data=%q", err, data)
				}
				return
			}
			if err != nil {
				t.Fatalf("hooks.log not written: %v", err)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(string(data), want) {
					t.Errorf("hooks.log missing %q:\n%s", want, data)
				}
			}
		})
	}
}

// TestHooks_HooksLogTruncatesPastCap asserts the size guard: an append against
// a hooks.log already past the cap truncates it first, so a persistently
// failing hook cannot grow the file without bound.
func TestHooks_HooksLogTruncatesPastCap(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t) // no run file written: every delivery fails
	logPath := filepath.Join(cfg.dataDir, "hooks.log")
	if err := os.MkdirAll(cfg.dataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	oversized := strings.Repeat("x", maxHooksLogBytes+1)
	if err := os.WriteFile(logPath, []byte(oversized), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := executeCLI(t, Deps{
		In: strings.NewReader(`{"reason":"logout"}`),
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must exit 0, got: %v", err)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > maxHooksLogBytes {
		t.Fatalf("hooks.log = %d bytes, want truncated below the %d cap", len(data), maxHooksLogBytes)
	}
	if !strings.Contains(string(data), "tallpine hooks claude-code session-end") {
		t.Errorf("truncated hooks.log missing the new failure line:\n%s", data)
	}
}

func TestHooks_DaemonErrorIsSwallowed(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusInternalServerError,
		`{"error":"internal","code":"BOOM","message":"boom"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"reason":"logout"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "session-end")
	if err != nil {
		t.Fatalf("hooks must exit 0 even on a daemon error, got: %v", err)
	}
	if !strings.Contains(errOut, "tallpine hooks") {
		t.Errorf("expected the failure surfaced to stderr, got %q", errOut)
	}
}

func TestHooks_DaemonNotRunningStaysOutOfStderr(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, cfg testConfig)
		alive func(int) bool
	}{
		{
			name: "stale run-file",
			setup: func(t *testing.T, cfg testConfig) {
				if err := runfile.Write(cfg.runFile, runfile.Info{
					PID: 999999, Port: 3101, StartedAt: time.Unix(100, 0).UTC(),
				}); err != nil {
					t.Fatalf("write run-file: %v", err)
				}
			},
			alive: func(int) bool { return false },
		},
		{
			name:  "no run-file",
			setup: func(t *testing.T, cfg testConfig) {},
			alive: func(int) bool { return true },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-7")
			cfg := setConfigEnv(t)
			tc.setup(t, cfg)

			_, errOut, err := executeCLI(t, Deps{
				In:           strings.NewReader(`{"reason":"logout"}`),
				ProcessAlive: tc.alive,
			}, "hooks", "claude-code", "session-end")
			if err != nil {
				t.Fatalf("hooks must exit 0 when the daemon is down, got: %v", err)
			}
			if errOut != "" {
				t.Errorf("daemon-down must not reach the agent's stderr, got %q", errOut)
			}

			logged, err := os.ReadFile(filepath.Join(cfg.dataDir, hooksLogName))
			if err != nil {
				t.Fatalf("daemon-down must still be recorded in hooks.log: %v", err)
			}
			if !strings.Contains(string(logged), "daemon is not running") {
				t.Errorf("hooks.log missing the daemon-down notice, got %q", logged)
			}
		})
	}
}

func TestHooks_ReviewerPermissionRequestAnswersInsteadOfBlocking(t *testing.T) {
	// Claude Code ≥ 2.1.257 prompts on Bash commands its analyzer cannot verify
	// even when an allow rule matches (#4810). A headless reviewer has nobody to
	// answer, so the hook decides: the exact submit shapes are allowed, anything
	// else is denied, and no blocked activity is reported either way.
	// The shell literal, JSON-escaped for the hook payload; `it'\''s` is the
	// prompt's shell-escaped single quote.
	const submitJSON = `'{ \"reviews\": [ { \"runId\": \"run-1\", \"verdict\": \"approved\", \"body\": \"it'\\''s fine\" } ] }'`
	cases := []struct {
		name    string
		payload string
		want    string
	}{
		{"tallpine review submit pipe", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' ` + submitJSON + ` | tallpine review submit --session worker-7 --reviews -"}}`, "allow"},
		{"gh api review post", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' '{ \"event\": \"COMMENT\", \"body\": \"ok\" }' | gh api --method POST repos/acme/app/pulls/12/reviews --input - --jq '.id'"}}`, "allow"},
		{"other worker session", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' '{}' | tallpine review submit --session worker-9 --reviews -"}}`, "deny"},
		{"unset worker session id", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' '{}' | tallpine review submit --session worker-7 --reviews -"}}`, "deny"},
		{"command substitution in operand", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' '{}'$(id) | tallpine review submit --session worker-7 --reviews -"}}`, "deny"},
		{"heredoc submit", `{"tool_name":"Bash","tool_input":{"command":"cat > /tmp/r.json <<'EOF'\n{}\nEOF\nao review submit --session worker-7 --reviews - < /tmp/r.json"}}`, "deny"},
		{"env inspection", `{"tool_name":"Bash","tool_input":{"command":"pip3 show pkg | sed -n 1p; cat \"$(pip3 show pkg)\" || python3 -c \"print(1)\""}}`, "deny"},
		{"non-bash tool", `{"tool_name":"Read","tool_input":{"file_path":"/etc/passwd"}}`, "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
			t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-7")
			t.Setenv(brand.Env("REVIEW_PRS"), "acme/app/12")
			if tc.name == "unset worker session id" {
				t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "")
			}
			t.Setenv("TALLPINE_REVIEW_HARNESS", "claude-code")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)

			out, errOut, err := executeCLI(t, Deps{
				In:           strings.NewReader(tc.payload),
				ProcessAlive: func(int) bool { return true },
			}, "hooks", "claude-code", "permission-request")
			if err != nil {
				t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
			}
			if capture.hits != 0 {
				t.Fatalf("reviewer permission-request reported activity; body=%s", capture.body)
			}
			var res claudePermissionHookOutput
			if err := json.Unmarshal([]byte(out), &res); err != nil {
				t.Fatalf("decode hook output: %v\nout=%s", err, out)
			}
			if res.HookSpecificOutput.HookEventName != "PermissionRequest" {
				t.Fatalf("hookEventName = %q", res.HookSpecificOutput.HookEventName)
			}
			if got := res.HookSpecificOutput.Decision.Behavior; got != tc.want {
				t.Fatalf("behavior = %q, want %q\nout=%s", got, tc.want, out)
			}
			if tc.want == "deny" && res.HookSpecificOutput.Decision.Message == "" {
				t.Fatalf("deny carried no message: %s", out)
			}
		})
	}
}

func TestHooksSessionDeliveryPreservesCoordinationOrigin(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tallpine-worker")
	t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	payload, _ := json.Marshal(map[string]string{"session_id": "native-1", "prompt": domain.WrapSessionDelivery("session-send:1", "orchestrator direction")})
	_, _, err := executeCLI(t, Deps{In: strings.NewReader(string(payload)), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", "user-prompt-submit")
	if err != nil {
		t.Fatal(err)
	}
	var req apitypes.SetActivityRequest
	if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
		t.Fatal(err)
	}
	if req.ConversationCheckpointOrigin != "coordination" || req.CoordinationID != "session-send:1" || req.LatestUserPrompt != "" {
		t.Fatalf("hook facts=%+v", req)
	}
}

func TestHooks_StalledDaemonFailsOpenWithinBudget(t *testing.T) {
	t.Setenv("TALLPINE_SESSION_ID", "tp-7")
	cfg := setConfigEnv(t)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	writeRunFileFor(t, cfg, srv)
	previous := hookReportBudget
	hookReportBudget = 100 * time.Millisecond
	t.Cleanup(func() { hookReportBudget = previous })

	started := time.Now()
	_, _, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"native-1","tool_name":"Bash","tool_use_id":"tu-1"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "claude-code", "pre-tool-use")
	if err != nil {
		t.Fatalf("a stalled daemon must not fail the hook: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("hook held the tool call for %s on a stalled daemon, want about the %s budget", elapsed, hookReportBudget)
	}
}

func TestReviewerPermissionDecision_BindsGhReviewPostToTheRunsPRs(t *testing.T) {
	post := func(path, body string) string {
		cmd := "printf '%s' '" + body + "' | gh api --method POST repos/" + path + "/reviews --input - --jq '.id'"
		b, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": cmd}})
		return string(b)
	}
	const allowed = "acme/app/12,acme/other/3"
	cases := []struct {
		name, payload, want string
	}{
		{"COMMENT on the run's PR", post("acme/app/pulls/12", `{"event":"COMMENT","body":"it'\''s ok"}`), "allow"},
		{"REQUEST_CHANGES on a queued PR", post("acme/other/pulls/3", `{"event":"REQUEST_CHANGES","body":"x"}`), "allow"},
		{"case-insensitive owner", post("Acme/App/pulls/12", `{"event":"COMMENT"}`), "allow"},
		{"cross-repo", post("victim-org/prod-infra/pulls/7", `{"event":"COMMENT","body":"x"}`), "deny"},
		{"other PR number", post("acme/app/pulls/13", `{"event":"COMMENT"}`), "deny"},
		{"APPROVE", post("acme/app/pulls/12", `{"event":"APPROVE","body":"lgtm"}`), "deny"},
		{"case-variant event override", post("acme/app/pulls/12", `{"event":"APPROVE","Event":"COMMENT"}`), "deny"},
		{"duplicate event key", post("acme/app/pulls/12", `{"event":"APPROVE","event":"COMMENT"}`), "deny"},
		{"no event", post("acme/app/pulls/12", `{"body":"x"}`), "deny"},
		{"invalid json", post("acme/app/pulls/12", `not json`), "deny"},
		{"chained", `{"tool_name":"Bash","tool_input":{"command":"printf '%s' '{\"event\":\"COMMENT\"}' | gh api --method POST repos/acme/app/pulls/12/reviews --input - --jq '.id'; id"}}`, "deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := reviewerPermissionDecision([]byte(tc.payload), "worker-7", allowed).HookSpecificOutput.Decision.Behavior
			if got != tc.want {
				t.Fatalf("behavior = %q, want %q", got, tc.want)
			}
		})
	}
	if got := reviewerPermissionDecision([]byte(post("acme/app/pulls/12", `{"event":"COMMENT"}`)), "worker-7", "").HookSpecificOutput.Decision.Behavior; got != "deny" {
		t.Fatalf("unset PR list allowed a post: %q", got)
	}
}

// dsh's Claude-compatible hooks plugin posts Claude-shaped payloads with an
// empty transcript_path; each event must reach the daemon with the right state.
func TestHooks_DeepSeekEvents(t *testing.T) {
	tests := []struct {
		event, payload, state, subagent string
	}{
		{"user-prompt-submit", `{"session_id":"dsh-1","transcript_path":"","prompt":"go"}`, "active", ""},
		{"pre-tool-use", `{"session_id":"dsh-1","transcript_path":"","tool_name":"bash","tool_use_id":"c1"}`, "active", ""},
		{"post-tool-use", `{"session_id":"dsh-1","transcript_path":"","tool_name":"bash","tool_use_id":"c1"}`, "active", ""},
		{"stop", `{"session_id":"dsh-1","transcript_path":"","stop_hook_active":false}`, "idle", ""},
		{"subagent-start", `{"session_id":"dsh-child","agent_id":"child-1","agent_type":"general-purpose"}`, "", "child-1"},
		{"subagent-stop", `{"session_id":"dsh-child","agent_id":"child-1","agent_type":"general-purpose"}`, "", "child-1"},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			t.Setenv("TALLPINE_SESSION_ID", "tp-9")
			t.Setenv("TALLPINE_RUNTIME_LAUNCH_ID", "launch-9")
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(tt.payload), ProcessAlive: func(int) bool { return true }},
				"hooks", "deepseek-harness", tt.event)
			if err != nil {
				t.Fatal(err)
			}
			var req apitypes.SetActivityRequest
			if err := json.Unmarshal([]byte(capture.body), &req); err != nil {
				t.Fatalf("no activity posted for %s: %v", tt.event, err)
			}
			if tt.subagent != "" && req.AgentSessionID != "" {
				t.Fatalf("child session id forwarded as main: %+v", req)
			}
			if tt.subagent == "" && req.AgentSessionID != "dsh-1" {
				t.Fatalf("main session id = %q", req.AgentSessionID)
			}
			if req.State != tt.state || req.SubagentID != tt.subagent || req.Event != tt.event || req.Harness != "deepseek-harness" {
				t.Fatalf("request = %+v", req)
			}
		})
	}
}

// A DeepSeek subagent event carries the CHILD's session id. The worker path
// drops it (it would replace the main resume id); the reviewer path always
// forwarded it, and a refactor must not change that.
func TestHooks_ReviewerKeepsDeepSeekSubagentSessionID(t *testing.T) {
	t.Setenv("TALLPINE_REVIEW_SESSION_ID", "review-7")
	t.Setenv("TALLPINE_REVIEW_WORKER_SESSION_ID", "worker-7")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true,"reviewSessionId":"review-7"}`)
	writeRunFileFor(t, cfg, srv)

	_, errOut, err := executeCLI(t, Deps{
		In:           strings.NewReader(`{"session_id":"child-sess","agent_id":"ds-child"}`),
		ProcessAlive: func(int) bool { return true },
	}, "hooks", "deepseek-harness", "subagent-start")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.path != "/api/v1/reviews/review-7/activity" {
		t.Fatalf("path = %q", capture.path)
	}
	if got := capturedAgentSessionID(t, capture); got != "child-sess" {
		t.Fatalf("reviewer agentSessionId = %q, want child-sess (raw payload id)", got)
	}
}
