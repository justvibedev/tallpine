package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// TestToAPIErrorMapsWorkspaceBranchSentinels covers Bug 3: the workspace
// adapter's typed branch errors map to typed envelope errors instead of
// collapsing to a 500.
func TestToAPIErrorMapsWorkspaceBranchSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantKind apierr.Kind
		wantCode string
	}{
		{"checked out elsewhere", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" is checked out at \"/tmp\"", ports.ErrWorkspaceBranchCheckedOutElsewhere), apierr.KindConflict, "BRANCH_CHECKED_OUT_ELSEWHERE"},
		{"default branch unresolved", fmt.Errorf("spawn mer-1: %w: configure defaultBranch", ports.ErrWorkspaceDefaultBranchUnresolved), apierr.KindInvalid, "DEFAULT_BRANCH_UNRESOLVED"},
		{"not fetched", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" has no local head", ports.ErrWorkspaceBranchNotFetched), apierr.KindInvalid, "BRANCH_NOT_FETCHED"},
		{"invalid branch", fmt.Errorf("spawn mer-1: workspace: %w: \"bad!!\" (exit 1)", ports.ErrWorkspaceBranchInvalid), apierr.KindInvalid, "INVALID_BRANCH"},
		{"agent binary not found", fmt.Errorf("spawn mer-1: %w", ports.ErrAgentBinaryNotFound), apierr.KindInvalid, "AGENT_BINARY_NOT_FOUND"},
		{"runtime prerequisite missing", fmt.Errorf("spawn: %w: tmux required on macOS/Linux but not in PATH", ports.ErrRuntimePrerequisite), apierr.KindInvalid, "RUNTIME_PREREQUISITE_MISSING"},
		{"Windows command line too long", fmt.Errorf("spawn: %w: escaped command line is 32769 UTF-16 code units", ports.ErrRuntimeCommandLineTooLong), apierr.KindInvalid, "WINDOWS_COMMAND_LINE_TOO_LONG"},
		{"runtime workspace cwd mismatch", fmt.Errorf("spawn mer-1: runtime: %w: session mer-1 started in \"/deleted/shipit\", want \"/tmp/ws\"", ports.ErrRuntimeWorkspaceCwdMismatch), apierr.KindConflict, "WORKSPACE_CWD_MISMATCH"},
		{"workspace locked", fmt.Errorf("restore mer-1: %w: \"/tmp/ws\" (branch \"tp/mer-1\") is registered but its directory is missing", ports.ErrWorkspaceLocked), apierr.KindConflict, "WORKSPACE_LOCKED"},
		{"cleanup script failed", fmt.Errorf("kill mer-1: %w: secret output", sessionmanager.ErrCleanupScript), apierr.KindConflict, "WORKSPACE_CLEANUP_FAILED"},
		{"unknown harness", fmt.Errorf("spawn: %w: %q", sessionmanager.ErrUnknownHarness, "bogus"), apierr.KindInvalid, "UNKNOWN_HARNESS"},
		{"missing harness", fmt.Errorf("spawn: %w: configure project worker.agent or pass --harness", sessionmanager.ErrMissingHarness), apierr.KindInvalid, "AGENT_REQUIRED"},
		{"artifact output unavailable", fmt.Errorf("spawn: %w", sessionmanager.ErrArtifactOutputUnavailable), apierr.KindInvalid, "ARTIFACT_OUTPUT_UNAVAILABLE"},
		{"harness install active", fmt.Errorf("spawn: %w", sessionmanager.ErrHarnessInstallActive), apierr.KindConflict, "HARNESS_INSTALL_ACTIVE"},
		{"awaiting decision", fmt.Errorf("send mer-1: %w", sessionmanager.ErrAwaitingDecision), apierr.KindConflict, "SESSION_AWAITING_DECISION"},
		{"delivery unconfirmed", fmt.Errorf("send mer-1: %w", sessionmanager.ErrDeliveryUnconfirmed), apierr.KindConflict, "SESSION_DELIVERY_UNCONFIRMED"},
		{"startup pending", fmt.Errorf("send mer-1: %w", sessionmanager.ErrStartupPending), apierr.KindConflict, "SESSION_STARTUP_PENDING"},
		{"composer busy", fmt.Errorf("send mer-1: %w", sessionmanager.ErrComposerBusy), apierr.KindConflict, "SESSION_COMPOSER_BUSY"},
		{"agent exited", fmt.Errorf("send mer-1: %w", sessionmanager.ErrAgentExited), apierr.KindConflict, "AGENT_EXITED"},
		{"agent stopped", fmt.Errorf("send mer-1: %w", sessionmanager.ErrAgentStoppedByUser), apierr.KindConflict, "AGENT_STOPPED"},
		{"agent not exited", fmt.Errorf("resume agent mer-1: %w", sessionmanager.ErrAgentNotExited), apierr.KindConflict, "AGENT_NOT_EXITED"},
		{"resume model denied", fmt.Errorf("resume agent mer-1: %w", sessionmanager.ErrResumeModelDenied), apierr.KindForbidden, "MODEL_DENIED"},
		{"resume fallback not saved", fmt.Errorf("resume agent mer-1: %w", sessionmanager.ErrResumeFallbackNotSaved), apierr.KindUnavailable, "MODEL_FALLBACK_NOT_SAVED"},
		{"resume model policy unreadable", fmt.Errorf("resume agent mer-1: %w", sessionmanager.ErrResumeModelPolicyUnavailable), apierr.KindUnavailable, "MODEL_POLICY_UNAVAILABLE"},
		{"resume in progress", fmt.Errorf("resume agent mer-1: %w", sessionmanager.ErrResumeInProgress), apierr.KindConflict, "AGENT_RESUME_IN_PROGRESS"},
		{"provider resume failed", fmt.Errorf("resume agent mer-1: %w", ports.ErrChatResumeFailed), apierr.KindConflict, "CHAT_RESUME_FAILED"},
		{"chat mode unsupported", fmt.Errorf("spawn: %w", ports.ErrChatUnsupported), apierr.KindConflict, "SESSION_MODE_UNSUPPORTED"},
		{"chat recovery inconclusive with deadline", fmt.Errorf("resume agent mer-1: resume chat: %w: persistent ACP host: %w",
			ports.ErrChatRecoveryInconclusive, fmt.Errorf("chat host ownership is inconclusive: %w", context.DeadlineExceeded)),
			apierr.KindConflict, "CHAT_RECOVERY_INCONCLUSIVE"},
		{"chat driver unavailable", fmt.Errorf("spawn: %w", ports.ErrChatDriverUnavailable), apierr.KindConflict, "CHAT_DRIVER_UNAVAILABLE"},
		{"chat driver incompatible", fmt.Errorf("spawn: %w", ports.ErrChatDriverIncompatible), apierr.KindConflict, "CHAT_DRIVER_INCOMPATIBLE"},
		{"chat auth required", fmt.Errorf("spawn: %w", ports.ErrChatAuthRequired), apierr.KindConflict, "CHAT_AUTH_REQUIRED"},
		{"agent auth required", fmt.Errorf("spawn: %w", ports.ErrAgentAuthRequired), apierr.KindConflict, "AGENT_AUTH_REQUIRED"},
		{"interface notice not acknowledgeable", fmt.Errorf("acknowledge interface notice: %w", sessionmanager.ErrInterfaceTransitionNoticeNotAcknowledgeable), apierr.KindConflict, "INTERFACE_TRANSITION_NOTICE_NOT_ACKNOWLEDGEABLE"},
		{"provider history recovery unavailable", fmt.Errorf("recover interface: %w", sessionmanager.ErrInterfaceProviderHistoryRecoveryUnavailable), apierr.KindConflict, "PROVIDER_HISTORY_RECOVERY_UNAVAILABLE"},
		{"native conversation missing", fmt.Errorf("switch interface: %w", sessionmanager.ErrNativeConversationMissing), apierr.KindConflict, "NATIVE_SESSION_MISSING"},
		{"native conversation unverified", fmt.Errorf("switch interface: %w", sessionmanager.ErrNativeConversationUnverified), apierr.KindConflict, "NATIVE_SESSION_UNVERIFIED"},
		{"account keeps its own history", fmt.Errorf("move: %w", &ports.SplitHistoryError{Home: "/accounts/b", Dirs: []string{"projects"}}), apierr.KindConflict, "AGENT_ACCOUNT_SPLIT_HISTORY"},
		{"unsupported effort", fmt.Errorf("spawn: %w", ports.ErrUnsupportedEffort), apierr.KindInvalid, "UNSUPPORTED_EFFORT"},
		{"model capabilities unavailable", fmt.Errorf("spawn: %w", ports.ErrModelCapabilitiesUnavailable), apierr.KindInvalid, "MODEL_CAPABILITIES_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := toAPIError(tc.err)
			var e *apierr.Error
			if !errors.As(mapped, &e) || e.Kind != tc.wantKind || e.Code != tc.wantCode {
				t.Fatalf("mapped = %v, want %s %s", mapped, tc.wantCode, e)
			}
			if strings.Contains(e.Message, "secret output") {
				t.Fatalf("mapped message leaked script output: %q", e.Message)
			}
		})
	}
}

func TestToSpawnAPIErrorMapsSpawnStageSentinels(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		err      error
		wantKind apierr.Kind
		wantCode string
	}{
		{
			"workspace create",
			fmt.Errorf("spawn mer-1: %w: git worktree add failed", sessionmanager.ErrWorkspaceCreate),
			apierr.KindConflict,
			"WORKSPACE_CREATE_FAILED",
		},
		{
			"workspace provision",
			fmt.Errorf("spawn mer-1: %w: postCreate \"pnpm install\": exit 1", sessionmanager.ErrWorkspaceProvision),
			apierr.KindConflict,
			"WORKSPACE_PROVISION_FAILED",
		},
		{
			"runtime create",
			fmt.Errorf("spawn mer-1: %w: tmux runtime: create session mer-1: context deadline exceeded", sessionmanager.ErrRuntimeCreate),
			apierr.KindInternal,
			"RUNTIME_CREATE_FAILED",
		},
		{
			"chat controller",
			fmt.Errorf("spawn mer-1: %w: app-server exited", sessionmanager.ErrChatController),
			apierr.KindConflict,
			"CHAT_CONTROLLER_FAILED",
		},
		{
			"spawn timeout",
			context.DeadlineExceeded,
			apierr.KindConflict,
			"SPAWN_TIMEOUT",
		},
		{
			"timeout wins over runtime stage",
			fmt.Errorf("spawn mer-1: %w: %w", sessionmanager.ErrRuntimeCreate, context.DeadlineExceeded),
			apierr.KindConflict,
			"SPAWN_TIMEOUT",
		},
		{
			"spawn cancelled",
			context.Canceled,
			apierr.KindConflict,
			"SPAWN_CANCELLED",
		},
		{
			"branch sentinel wins over workspace stage",
			fmt.Errorf("spawn mer-1: %w: %w", sessionmanager.ErrWorkspaceCreate, ports.ErrWorkspaceBranchNotFetched),
			apierr.KindInvalid,
			"BRANCH_NOT_FETCHED",
		},
		{
			"artifact output without a data dir",
			fmt.Errorf("spawn: %w", sessionmanager.ErrArtifactOutputUnavailable),
			apierr.KindInvalid,
			"ARTIFACT_OUTPUT_UNAVAILABLE",
		},
		{
			"unclassified fallback",
			errors.New("boom"),
			apierr.KindInternal,
			"SPAWN_INTERNAL",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := toSpawnAPIError(tc.err)
			var e *apierr.Error
			if !errors.As(mapped, &e) || e.Kind != tc.wantKind || e.Code != tc.wantCode {
				t.Fatalf("mapped = %v, want kind=%v code=%s", mapped, tc.wantKind, tc.wantCode)
			}
		})
	}
}

func TestToSpawnAPIErrorIsIdempotentForMappedErrors(t *testing.T) {
	t.Parallel()

	raw := fmt.Errorf("spawn mer-1: %w: tmux runtime: create session mer-1: boom", sessionmanager.ErrRuntimeCreate)
	first := toSpawnAPIError(raw)
	second := toSpawnAPIError(first)

	var firstErr, secondErr *apierr.Error
	if !errors.As(first, &firstErr) || !errors.As(second, &secondErr) {
		t.Fatalf("mapped = %v / %v, want *apierr.Error", first, second)
	}
	if firstErr.Code != "RUNTIME_CREATE_FAILED" || secondErr.Code != "RUNTIME_CREATE_FAILED" {
		t.Fatalf("codes = %q / %q, want RUNTIME_CREATE_FAILED", firstErr.Code, secondErr.Code)
	}
}

func TestToAPIErrorPreservesMissingChatCapabilityRecoveryDetails(t *testing.T) {
	t.Parallel()

	mapped := toAPIError(fmt.Errorf("spawn: %w", &ports.ChatCapabilityError{
		Harness:                domain.HarnessKiro,
		Missing:                []ports.ChatCapability{ports.ChatCapabilityApprovals},
		AllowedPermissionModes: []ports.PermissionMode{ports.PermissionModeBypassPermissions},
	}))

	var apiError *apierr.Error
	if !errors.As(mapped, &apiError) {
		t.Fatalf("mapped = %v, want *apierr.Error", mapped)
	}
	if apiError.Code != "SESSION_MODE_UNSUPPORTED" {
		t.Fatalf("code = %q, want SESSION_MODE_UNSUPPORTED", apiError.Code)
	}
	missing, ok := apiError.Details["missingCapabilities"].([]string)
	if !ok || len(missing) != 1 || missing[0] != "approvals" {
		t.Fatalf("missingCapabilities = %#v, want [approvals]", apiError.Details["missingCapabilities"])
	}
	allowed, ok := apiError.Details["allowedApprovalModes"].([]string)
	if !ok || len(allowed) != 1 || allowed[0] != "bypass-permissions" {
		t.Fatalf("allowedApprovalModes = %#v, want [bypass-permissions]", apiError.Details["allowedApprovalModes"])
	}
}

// TestToAPIError_NotResumable asserts that ErrNotResumable (promptless worker
// with no adapter resume handle) maps to a Conflict with code SESSION_NOT_RESUMABLE.
func TestToAPIError_NotResumable(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("restore mer-1: %w", sessionmanager.ErrNotResumable)
	mapped := toAPIError(err)
	var e *apierr.Error
	if !errors.As(mapped, &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_NOT_RESUMABLE" {
		t.Fatalf("mapped = %v, want Conflict SESSION_NOT_RESUMABLE", mapped)
	}
}

func TestRestoreMapsManagerModeToServiceView(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityIdle},
	}
	fc := &fakeCommander{
		restoreResult: sessionmanager.RestoreResult{
			Session: rec,
			Mode:    sessionmanager.RestoreModeSavedPrompt,
		},
	}
	svc := &Service{manager: fc, store: st}

	got, err := svc.Restore(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got.Session.ID != "mer-1" {
		t.Fatalf("session id = %q, want mer-1", got.Session.ID)
	}
	if got.Mode != RestoreModeViewSavedPrompt {
		t.Fatalf("mode = %q, want %q", got.Mode, RestoreModeViewSavedPrompt)
	}
}

func TestResumeAgentMapsManagerModeToServiceView(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityIdle},
	}
	fc := &fakeCommander{
		restoreResult: sessionmanager.RestoreResult{
			Session: rec,
			Mode:    sessionmanager.RestoreModeNative,
		},
	}
	svc := &Service{manager: fc, store: st}

	got, err := svc.ResumeAgent(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ResumeAgent: %v", err)
	}
	if got.Session.ID != "mer-1" || got.Mode != RestoreModeViewNative {
		t.Fatalf("resume outcome = %+v", got)
	}
}

func TestExitAgentPreservesSessionAndMapsExitedReadModel(t *testing.T) {
	t.Parallel()

	st := newFakeStore()
	rec := domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityIdle},
	}
	fc := &fakeCommander{restoreResult: sessionmanager.RestoreResult{Session: rec}}
	svc := &Service{manager: fc, store: st}

	got, err := svc.ExitAgent(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("ExitAgent: %v", err)
	}
	if got.Session.ID != "mer-1" || got.Session.IsTerminated || got.Session.Activity.State != domain.ActivityExited {
		t.Fatalf("exit outcome = %+v", got)
	}
	if len(fc.exited) != 1 || fc.exited[0] != "mer-1" {
		t.Fatalf("exit calls = %v", fc.exited)
	}
}
