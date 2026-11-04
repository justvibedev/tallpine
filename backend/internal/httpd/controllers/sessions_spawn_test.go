package controllers_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
)

func TestSessionsAPI_SetReviewerAllowsConfigWithoutHarness(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/tp-1/reviewer", `{"agentConfig":{"model":"gpt-5"}}`)
	if status != http.StatusOK {
		t.Fatalf("set reviewer with default harness override = %d, want 200; body=%s", status, body)
	}
	if got := svc.sessions["tp-1"]; got.ReviewerHarness != "" || got.ReviewerConfig.Model != "gpt-5" {
		t.Fatalf("reviewer update persisted = (%q, %+v), want default harness with model override", got.ReviewerHarness, got.ReviewerConfig)
	}
}

func TestSessionsAPI_SetAutoInjectCIValidatesBody(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		wantCode string
	}{
		{name: "malformed JSON", body: `{`, wantCode: "INVALID_JSON"},
		{name: "missing autoInjectCI", body: `{}`, wantCode: "AUTO_INJECT_CI_REQUIRED"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/tp-1/auto-inject-ci", tt.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tt.wantCode)
			if svc.autoInjectCISession != "" {
				t.Fatalf("SetAutoInjectCI called for invalid body with session %q", svc.autoInjectCISession)
			}
		})
	}
}

func TestSessionsAPI_SpawnRejectsOversizedBody(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	// A body past the ~135 MiB maxSpawnBodyBytes cap is rejected while decoding
	// (MaxBytesReader), before the attachment size caps and without materializing
	// the whole body. The oversized bytes live in an *attachment* payload (not the
	// prompt, which has its own much smaller PROMPT_TOO_LONG cap), so this pins the
	// body cap specifically: MaxBytesReader makes the read/decode fail with
	// INVALID_JSON. If that line were removed the body would decode fully and be
	// rejected later with an attachment-specific code (ATTACHMENT_TOO_LARGE),
	// failing this test. 150 MiB of base64 comfortably exceeds the ~135 MiB cap.
	oversized := `{"projectId":"tp","attachments":[{"mimeType":"image/png","data":"` +
		strings.Repeat("A", 150<<20) + `"}]}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", oversized)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

func TestSessionsAPI_SpawnAcceptsVideoAbovePreviousLimit(t *testing.T) {
	const size = 11 << 20
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	video := base64.StdEncoding.EncodeToString(make([]byte, size))
	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","prompt":"inspect the video","attachments":[{"mimeType":"video/quicktime","data":"`+video+`"}]}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn with 11 MiB video = %d, want 201; body=%s", status, body)
	}
	if len(svc.lastSpawn.Attachments) != 1 {
		t.Fatalf("spawn attachments = %d, want one", len(svc.lastSpawn.Attachments))
	}
	if got := svc.lastSpawn.Attachments[0]; got.Ext != ".mov" || len(got.Data) != size {
		t.Fatalf("spawn attachment extension = %q, bytes = %d; want .mov and %d", got.Ext, len(got.Data), size)
	}
}

func TestSessionsAPI_SpawnRejectsUnknownExplicitMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","prompt":"fix","mode":"tuii"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "SESSION_MODE_INVALID")
	if len(svc.sessions) != 1 {
		t.Fatalf("invalid mode created a session: %#v", svc.sessions)
	}
}

func TestSessionsAPI_SpawnsOMPChat(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","harness":"omp","mode":"chat","prompt":"fix"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn OMP Chat = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.Harness != domain.AgentHarness("omp") || svc.lastSpawn.RequestedMode != domain.SessionModeChat {
		t.Fatalf("spawn config = %#v, want OMP Chat", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnsOpenCodeV2(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","harness":"opencode-v2","mode":"tui","prompt":"fix"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn OpenCode 2 = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.Harness != domain.AgentHarness("opencode-v2") || svc.lastSpawn.RequestedMode != domain.SessionModeTUI {
		t.Fatalf("spawn config = %#v, want OpenCode 2 TUI", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnPreservesUnknownHarnessErrorEnvelope(t *testing.T) {
	svc := newFakeSessionService()
	svc.spawnErr = apierr.Invalid("UNKNOWN_HARNESS", "Unknown agent harness", nil)
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","harness":"not-a-harness","mode":"tui","prompt":"fix"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid harness status = %d, want 400; body=%s", status, body)
	}
	var got struct {
		Error     string `json:"error"`
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"requestId"`
	}
	mustJSON(t, body, &got)
	if got.Error != "bad_request" || got.Code != "UNKNOWN_HARNESS" || got.Message != "Unknown agent harness" || got.RequestID == "" {
		t.Fatalf("invalid harness envelope = %#v", got)
	}
}

func TestSessionsAPI_SpawnsStandaloneWorkerWithoutProjectID(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"kind":"worker","harness":"codex","prompt":"research","displayName":"Research"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn standalone = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.ProjectID != "" || svc.lastSpawn.Kind != domain.KindWorker || svc.lastSpawn.Harness != domain.HarnessCodex {
		t.Fatalf("spawn config = %#v, want projectless codex worker", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnsStandaloneUnrealChatWithApprovalMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"kind":"worker","harness":"unreal-agent","mode":"chat","approvalMode":"bypass-permissions","prompt":"hello"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn Unreal Chat = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.Harness != domain.AgentHarness("unreal-agent") || svc.lastSpawn.RequestedMode != domain.SessionModeChat || svc.lastSpawn.AgentConfig.Permissions != domain.PermissionModeBypassPermissions {
		t.Fatalf("spawn config = %#v, want Unreal Chat with bypass permissions", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnsQwenChat(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions",
		`{"projectId":"tp","harness":"qwen","mode":"chat","prompt":"fix"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn Qwen Chat = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.Harness != domain.AgentHarness("qwen") || svc.lastSpawn.RequestedMode != domain.SessionModeChat {
		t.Fatalf("spawn config = %#v, want Qwen Chat", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnPassesModelToService(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"codex","prompt":"fix","displayName":"my worker","model":"sonnet"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.AgentConfig.Model != "sonnet" {
		t.Fatalf("service AgentConfig.Model = %q, want sonnet", svc.lastSpawn.AgentConfig.Model)
	}
}

func TestSessionsAPI_SpawnPassesEffortToService(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"kind":"worker","harness":"codex","prompt":"fix","displayName":"my worker","effort":"high"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.AgentConfig.Effort != "high" {
		t.Fatalf("service AgentConfig.Effort = %q, want high", svc.lastSpawn.AgentConfig.Effort)
	}
	// A request-carried effort is an explicit override, so the service can
	// distinguish it from an inherited default and reject harnesses that
	// cannot honor it instead of silently dropping the caller's choice.
	if !svc.lastSpawn.EffortOverride {
		t.Fatal("service EffortOverride = false, want true for a request-carried effort")
	}

	// Omitted effort must not masquerade as an explicit override.
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"kind":"worker","harness":"codex","prompt":"fix","displayName":"second","model":"sonnet"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.EffortOverride {
		t.Fatal("service EffortOverride = true for omitted effort, want false")
	}
}

func TestSessionsAPI_SpawnPassesParentSessionToService(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"tp","kind":"worker","harness":"claude-code","parentSessionId":"tp-1","prompt":"fix"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.ParentSessionID != "tp-1" {
		t.Fatalf("service ParentSessionID = %q, want tp-1", svc.lastSpawn.ParentSessionID)
	}
}

func TestSessionsAPI_OrchestratorAcceptsExplicitChatMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators",
		`{"projectId":"tp","mode":"chat"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	if svc.orchestratorMode != domain.SessionModeChat {
		t.Fatalf("requested mode = %q, want chat", svc.orchestratorMode)
	}
}

func TestSessionsAPI_OrchestratorRejectsUnknownExplicitMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators",
		`{"projectId":"tp","mode":"chatt"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "SESSION_MODE_INVALID")
}

func TestSessionsAPI_OrchestratorAcceptsApprovalOverride(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators",
		`{"projectId":"tp","mode":"chat","approvalMode":"bypass-permissions"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	if svc.orchestratorApproval != domain.PermissionModeBypassPermissions {
		t.Fatalf("approval = %q, want bypass-permissions", svc.orchestratorApproval)
	}
}

func TestSessionsAPI_OrchestratorRejectsUnknownApprovalMode(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators",
		`{"projectId":"tp","approvalMode":"sometimes"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_APPROVAL_MODE")
}

func TestSessionsAPI_SpawnAcceptsArtifactRequestedOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "project", body: `{"projectId":"tp","kind":"worker","harness":"codex","requestedOutput":"artifact","displayName":"New artifact"}`},
		{name: "standalone", body: `{"kind":"worker","harness":"codex","requestedOutput":"artifact","displayName":"New artifact"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", tc.body)
			if status != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", status, body)
			}
			if svc.lastSpawn.RequestedOutput != domain.RequestedOutputArtifact {
				t.Fatalf("RequestedOutput = %q, want artifact", svc.lastSpawn.RequestedOutput)
			}
		})
	}
}

func TestSessionsAPI_SpawnOmittedRequestedOutputStaysEmpty(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	if svc.lastSpawn.RequestedOutput != "" {
		t.Fatalf("RequestedOutput = %q, want empty", svc.lastSpawn.RequestedOutput)
	}
}

func TestSessionsAPI_SpawnRejectsUnknownRequestedOutput(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","kind":"worker","harness":"codex","requestedOutput":"slides"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_REQUESTED_OUTPUT")
	if svc.lastSpawn.RequestedOutput != "" || svc.lastSpawn.ProjectID != "" {
		t.Fatalf("rejected request reached the service: %#v", svc.lastSpawn)
	}
}

func TestSessionsAPI_SpawnBranchNotFetchedReturnsTypedError(t *testing.T) {
	svc := newFakeSessionService()
	svc.spawnErr = apierr.Invalid("BRANCH_NOT_FETCHED", `workspace: branch is not fetched: "feature/missing"`, nil)
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","kind":"worker","branch":"feature/missing","prompt":"fix"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "BRANCH_NOT_FETCHED")
}

// TestSessionsAPI_SpawnRejectsOverlongDisplayName asserts the spawn endpoint
// caps displayName at 100 characters even though the field itself is optional
// (the desktop new-task dialog omits it). `tallpine spawn` enforces the same limit
// CLI-side before the request is sent.
func TestSessionsAPI_SpawnRejectsOverlongDisplayName(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	exact := strings.Repeat("x", 100)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","harness":"codex","displayName":"`+exact+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("spawn 100-char displayName = %d, want 201; body=%s", status, body)
	}

	overlong := strings.Repeat("x", 101)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"tp","harness":"codex","displayName":"`+overlong+`"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "DISPLAY_NAME_TOO_LONG")
}

func TestSessionsAPI_DelegateTask(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"Fix\u0000 it","agent":"cursor","model":" sonnet-custom ","effort":" high ","mode":"chat","approvalMode":"bypass-permissions","taskPreparation":" prep-token ","attachments":[{"mimeType":"image/png","data":"AQID"}]}`)
	if status != http.StatusAccepted {
		t.Fatalf("delegate = %d, want 202; body=%s", status, body)
	}
	var got struct {
		OK             bool   `json:"ok"`
		WorkerID       string `json:"workerId"`
		OrchestratorID string `json:"orchestratorId"`
	}
	mustJSON(t, body, &got)
	if !got.OK || got.WorkerID != "tallpine-worker" || got.OrchestratorID != "tp-orch" {
		t.Fatalf("response = %#v", got)
	}
	if svc.delegationInput.ProjectID != "tp" || svc.delegationInput.Brief != "Fix it" || svc.delegationInput.RequestedAgent != domain.AgentHarness("cursor") || svc.delegationInput.Model != "sonnet-custom" || svc.delegationInput.Effort == nil || *svc.delegationInput.Effort != "high" || svc.delegationInput.RequestedMode != domain.SessionModeChat || svc.delegationInput.ApprovalMode != domain.PermissionModeBypassPermissions || svc.delegationInput.TaskPreparation != "prep-token" {
		t.Fatalf("delegation input = %#v", svc.delegationInput)
	}
	if len(svc.delegationInput.Attachments) != 1 {
		t.Fatalf("attachments = %#v, want one", svc.delegationInput.Attachments)
	}
	if got := svc.delegationInput.Attachments[0]; got.Ext != ".png" || string(got.Data) != "\x01\x02\x03" {
		t.Fatalf("attachment = %#v, want decoded png", got)
	}
}

func TestSessionsAPI_DelegateClientRequestHashIgnoresPreparationButBindsTask(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	var firstHash string
	for i, payload := range []string{
		`{"projectId":"tp","brief":"Fix it","taskPreparation":"prep-a","clientRequestId":"draft-1"}`,
		`{"projectId":"tp","brief":"Fix it","taskPreparation":"prep-b","clientRequestId":"draft-1"}`,
		`{"projectId":"tp","brief":"Fix something else","taskPreparation":"prep-b","clientRequestId":"draft-1"}`,
	} {
		_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", payload)
		if status != http.StatusAccepted {
			t.Fatalf("request %d status = %d", i, status)
		}
		hash := svc.delegationInput.ClientRequestHash
		if svc.delegationInput.ClientRequestID != "draft-1" || hash == "" {
			t.Fatalf("request %d id/hash = %q/%q", i, svc.delegationInput.ClientRequestID, hash)
		}
		if i == 0 {
			firstHash = hash
		} else if (i == 1) != (hash == firstHash) {
			t.Fatalf("request %d hash = %q, first = %q", i, hash, firstHash)
		}
	}
}

func TestSessionsAPI_PreparesAndCancelsTaskWorkspace(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/projects/tp/tasks/prepare", "")
	if status != http.StatusAccepted || svc.preparedProject != "tp" || !strings.Contains(string(body), `"taskPreparation":"prep-token"`) {
		t.Fatalf("prepare = %d %s, project %q", status, body, svc.preparedProject)
	}
	_, status, _ = doRequest(t, srv, http.MethodDelete, "/api/v1/task-preparations/prep-token", "")
	if status != http.StatusNoContent || svc.canceledPreparation != "prep-token" {
		t.Fatalf("cancel = %d, token %q", status, svc.canceledPreparation)
	}
}

func TestSessionsAPI_DelegateTaskAcceptsLongBrief(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	brief := strings.Repeat("a", 4097)
	payload, err := json.Marshal(map[string]string{
		"projectId": "tp",
		"brief":     brief,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", string(payload))
	if status != http.StatusAccepted {
		t.Fatalf("delegate long brief = %d, want 202; body=%s", status, body)
	}
	if svc.delegationInput.Brief != brief {
		t.Fatalf("delegation brief length = %d, want %d", len(svc.delegationInput.Brief), len(brief))
	}
}

func TestSessionsAPI_DelegateTaskRejectsBriefPastLaunchSafeLimit(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)
	brief := strings.Repeat("a", (16<<10)+1)
	payload, err := json.Marshal(map[string]string{
		"projectId": "tp",
		"brief":     brief,
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate", string(payload))
	assertErrorCode(t, body, status, http.StatusBadRequest, "TASK_TOO_LONG")
	var got struct {
		Message string `json:"message"`
	}
	mustJSON(t, body, &got)
	if got.Message != "Task must be 16 KiB or fewer" {
		t.Fatalf("error message = %q, want actionable size limit", got.Message)
	}
	if svc.delegationInput.ProjectID != "" {
		t.Fatalf("delegate service called with oversized brief: %#v", svc.delegationInput)
	}
}

func TestSessionsAPI_DelegatesOMPChat(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate",
		`{"projectId":"tp","brief":"Fix it","agent":"omp","mode":"chat"}`)
	if status != http.StatusAccepted {
		t.Fatalf("delegate OMP Chat = %d, want 202; body=%s", status, body)
	}
	if svc.delegationInput.RequestedAgent != domain.AgentHarness("omp") || svc.delegationInput.RequestedMode != domain.SessionModeChat {
		t.Fatalf("delegation input = %#v, want OMP Chat", svc.delegationInput)
	}
}

func TestSessionsAPI_DelegatesQwenChat(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/orchestrators/delegate",
		`{"projectId":"tp","brief":"Fix it","agent":"qwen","mode":"chat"}`)
	if status != http.StatusAccepted {
		t.Fatalf("delegate Qwen Chat = %d, want 202; body=%s", status, body)
	}
	if svc.delegationInput.RequestedAgent != domain.AgentHarness("qwen") || svc.delegationInput.RequestedMode != domain.SessionModeChat {
		t.Fatalf("delegation input = %#v, want Qwen Chat", svc.delegationInput)
	}
}

func TestSessionsAPI_DelegateTaskValidationAndServiceError(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":""}`)
	if status != http.StatusAccepted {
		t.Fatalf("promptless delegate = %d, want 202; body=%s", status, body)
	}
	if svc.delegationInput.ProjectID != "tp" || svc.delegationInput.Brief != "" {
		t.Fatalf("promptless delegation input = %#v", svc.delegationInput)
	}

	svc.delegationErr = apierr.Invalid("UNKNOWN_HARNESS", "Unknown requested agent", nil)
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"Fix it"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "UNKNOWN_HARNESS")

	svc.delegationErr = nil
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"Fix it","mode":"tuii"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_SESSION_MODE")

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", `{"projectId":"tp","brief":"Fix it","approvalMode":"sometimes"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_APPROVAL_MODE")
	var approvalError struct {
		Error   string `json:"error"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	mustJSON(t, body, &approvalError)
	if approvalError.Error != "bad_request" || approvalError.Code != "INVALID_APPROVAL_MODE" || approvalError.Message != "approvalMode is invalid" {
		t.Fatalf("invalid approval envelope = %#v", approvalError)
	}
}

func TestSessionsAPI_DelegateTaskRejectsInvalidAttachments(t *testing.T) {
	tests := []struct {
		name string
		body string
		code string
	}{
		{
			name: "bad base64",
			body: `{"projectId":"tp","brief":"Fix it","attachments":[{"mimeType":"image/png","data":"!!!"}]}`,
			code: "INVALID_ATTACHMENT_DATA",
		},
		{
			name: "empty base64",
			body: `{"projectId":"tp","brief":"Fix it","attachments":[{"mimeType":"image/png","data":""}]}`,
			code: "INVALID_ATTACHMENT_DATA",
		},
		{
			name: "svg",
			body: `{"projectId":"tp","brief":"Fix it","attachments":[{"mimeType":"image/svg+xml","data":"PHN2Zy8+"}]}`,
			code: "UNSUPPORTED_ATTACHMENT_TYPE",
		},
		{
			name: "too large",
			body: `{"projectId":"tp","brief":"Fix it","attachments":[{"mimeType":"image/png","data":"` +
				base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", (50<<20)+1))) + `"}]}`,
			code: "ATTACHMENT_TOO_LARGE",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", tc.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
		})
	}
}

func TestSessionsAPI_DelegateTaskRejectsOversizedBody(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	// A body past the ~135 MiB spawn attachment cap is rejected while decoding
	// (MaxBytesReader), before attachment size validation and without
	// materializing the whole body. 150 MiB of base64 comfortably exceeds the cap.
	oversized := `{"projectId":"tp","brief":"Fix it","attachments":[{"mimeType":"image/png","data":"` +
		strings.Repeat("A", 150<<20) + `"}]}`
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/orchestrators/delegate", oversized)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
	if svc.delegationInput.ProjectID != "" {
		t.Fatalf("delegate service called with oversized body: %#v", svc.delegationInput)
	}
}

type plannerSwitch struct {
	controllers.BacklogPlanService
	on bool
}

func (p plannerSwitch) Enabled() bool { return p.on }

func TestSessionsAPI_SpawnRolePlannerFollowsTheSwitch(t *testing.T) {
	const body = `{"projectId":"tp","kind":"worker","harness":"codex","role":"planner","displayName":"Plan"}`
	for _, tc := range []struct {
		name string
		deps httpd.APIDeps
		want int
	}{
		{"switch off", httpd.APIDeps{BacklogPlans: plannerSwitch{on: false}}, http.StatusNotFound},
		{"unwired", httpd.APIDeps{}, http.StatusNotFound},
		{"switch on", httpd.APIDeps{BacklogPlans: plannerSwitch{on: true}}, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			tc.deps.Sessions = svc
			srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, tc.deps, httpd.ControlDeps{}))
			t.Cleanup(srv.Close)
			out, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", body)
			if status != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.want, out)
			}
			if tc.want == http.StatusNotFound && !strings.Contains(string(out), "PLANNER_DISABLED") {
				t.Fatalf("body = %s, want PLANNER_DISABLED", out)
			}
			if tc.want == http.StatusCreated && (svc.lastSpawn.Role != domain.SessionRolePlanner || svc.lastSpawn.RequestedOutput != domain.RequestedOutputArtifact) {
				t.Fatalf("spawn = %+v, want planner role with artifact output", svc.lastSpawn)
			}
		})
	}
}
