package session

import (
	"errors"
	"fmt"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

// sessionErrorRow is one sentinel the session service maps to an API error.
// An empty message means the API message is the wrapped error's own text.
type sessionErrorRow struct {
	sentinel error
	kind     string
	code     string
	message  string
}

func (r sessionErrorRow) want(err error) *apierr.Error {
	message := r.message
	if message == "" {
		message = err.Error()
	}
	switch r.kind {
	case "NotFound":
		return apierr.NotFound(r.code, message)
	case "Forbidden":
		return apierr.Forbidden(r.code, message)
	case "Unavailable":
		return apierr.Unavailable(r.code, message)
	case "Invalid":
		return apierr.Invalid(r.code, message, nil)
	default:
		return apierr.Conflict(r.code, message, nil)
	}
}

// sessionErrorRows lists the sentinels in match order. Order matters: an error
// that wraps two sentinels maps to the earlier row.
func sessionErrorRows() []sessionErrorRow {
	return []sessionErrorRow{
		{sessionmanager.ErrNotFound, "NotFound", "SESSION_NOT_FOUND", "Unknown session"},
		{sessionmanager.ErrSessionNotTerminated, "Conflict", "SESSION_NOT_TERMINATED", "Archive the session before deleting it."},
		{sessionmanager.ErrWorkspaceNotReleased, "Conflict", "WORKSPACE_NOT_RELEASED", "The session's worktree has not been cleaned up yet. Run `tallpine session cleanup` first, or delete with cleanup."},
		{domain.ErrSessionHistoryShared, "Conflict", "SESSION_HISTORY_SHARED", "This session's turns belong to a conversation that outlives it, so it cannot be deleted."},
		{ErrPRNotOpen, "Conflict", "PR_NOT_OPEN", "PR is not open"},
		{ports.ErrAgentAccountUnknown, "NotFound", "AGENT_ACCOUNT_NOT_FOUND", "Unknown agent account"},
		{ports.ErrAgentAccountProviderMismatch, "Invalid", "AGENT_ACCOUNT_PROVIDER_MISMATCH", "The account is for a different agent"},
		{ports.ErrAgentAccountDefaultUnusable, "Conflict", "AGENT_ACCOUNT_DEFAULT_UNUSABLE", "The default account is signed out or now holds another login, and no other account is signed in. Sign one in again in Settings."},
		{ports.ErrAgentAccountSignedOut, "Conflict", "AGENT_ACCOUNT_SIGNED_OUT", "The account is signed out; sign it in again first"},
		{ports.ErrAgentAccountUnchanged, "Conflict", "AGENT_ACCOUNT_UNCHANGED", "The session already uses this account"},
		{ports.ErrAgentAccountUnshared, "Conflict", "AGENT_ACCOUNT_UNSHARED", "Tallpine cannot link this account's folder to this computer's history. On Windows, turn on Developer Mode."},
		{sessionmanager.ErrNotRestorable, "Conflict", "SESSION_NOT_RESTORABLE", "Session is not restorable"},
		{sessionmanager.ErrTerminated, "Conflict", "SESSION_TERMINATED", "Session is terminated"},
		{sessionmanager.ErrResumeModelDenied, "Forbidden", "MODEL_DENIED", ""},
		{sessionmanager.ErrResumeFallbackNotSaved, "Unavailable", "MODEL_FALLBACK_NOT_SAVED", ""},
		{sessionmanager.ErrResumeModelPolicyUnavailable, "Unavailable", "MODEL_POLICY_UNAVAILABLE", ""},
		{sessionmanager.ErrAgentStoppedByUser, "Conflict", "AGENT_STOPPED", "The agent was stopped; resume the session before sending it messages"},
		{sessionmanager.ErrAgentExited, "Conflict", "AGENT_EXITED", "The agent process exited; relaunch it before sending another message"},
		{sessionmanager.ErrAgentNotExited, "Conflict", "AGENT_NOT_EXITED", "The agent is still running; only exited agents can be resumed"},
		{sessionmanager.ErrResumeInProgress, "Conflict", "AGENT_RESUME_IN_PROGRESS", "The agent is already being resumed"},
		{sessionmanager.ErrAgentExitInProgress, "Conflict", "AGENT_EXIT_IN_PROGRESS", "The agent is already exiting"},
		{sessionmanager.ErrOperationInProgress, "Conflict", "SESSION_OPERATION_IN_PROGRESS", "Another operation is already running on this session"},
		{sessionmanager.ErrInterfaceTransitionInProgress, "Conflict", "INTERFACE_TRANSITION_IN_PROGRESS", "This session is already switching interfaces"},
		{sessionmanager.ErrInterfaceHandoffUnsupported, "Conflict", "INTERFACE_HANDOFF_UNSUPPORTED", ""},
		{sessionmanager.ErrNativeConversationMissing, "Conflict", "NATIVE_SESSION_MISSING", "The agent has not exposed a native conversation that can resume in the other interface"},
		{sessionmanager.ErrNativeConversationUnverified, "Conflict", "NATIVE_SESSION_UNVERIFIED", "The current terminal launch has not confirmed that it owns the stored native conversation yet"},
		{sessionmanager.ErrInterfaceWorkspaceUnavailable, "Conflict", "WORKSPACE_UNAVAILABLE", "The session's workspace was never set up; retry its start before switching interfaces"},
		{sessionmanager.ErrInterfaceTransitionNotCancellable, "Conflict", "INTERFACE_TRANSITION_NOT_CANCELLABLE", "The source controller has already stopped; Tallpine must finish or recover the switch"},
		{sessionmanager.ErrInterfaceTransitionNoticeNotAcknowledgeable, "Conflict", "INTERFACE_TRANSITION_NOTICE_NOT_ACKNOWLEDGEABLE", "This interface switch has no failure or recovery notice to acknowledge"},
		{sessionmanager.ErrInterfaceProviderHistoryRecoveryUnavailable, "Conflict", "PROVIDER_HISTORY_RECOVERY_UNAVAILABLE", "Provider history can be used only after Tallpine identifies a legacy text-only mismatch"},
		{sessionmanager.ErrInterfaceAlreadySelected, "Conflict", "INTERFACE_ALREADY_SELECTED", "The session is already using the requested interface"},
		{sessionmanager.ErrInterfaceTransitionNotFound, "NotFound", "INTERFACE_TRANSITION_NOT_FOUND", "Interface switch not found"},
		{sessionmanager.ErrAwaitingDecision, "Conflict", "SESSION_AWAITING_DECISION", "Session is paused on a permission decision; answer it in the session terminal first"},
		{sessionmanager.ErrComposerBusy, "Conflict", "SESSION_COMPOSER_BUSY", "Session composer has an unsent draft; retry after the draft is submitted or cleared"},
		{sessionmanager.ErrStartupPending, "Conflict", "SESSION_STARTUP_PENDING", "Session agent is still starting; retry after the agent prompt is ready"},
		{sessionmanager.ErrIncompleteHandle, "Conflict", "SESSION_INCOMPLETE_HANDLE", "Session is missing runtime or workspace handles"},
		{sessionmanager.ErrNotResumable, "Conflict", "SESSION_NOT_RESUMABLE", "This session has no saved agent session or prompt to resume from"},
		{sessionmanager.ErrProjectNotResolvable, "Invalid", "PROJECT_NOT_RESOLVABLE", "Project is not registered or has no repo. Register it with `tallpine project add`"},
		{sessionmanager.ErrUnknownHarness, "Invalid", "UNKNOWN_HARNESS", ""},
		{sessionmanager.ErrUnsupportedHarness, "Conflict", "HARNESS_UNSUPPORTED", ""},
		{sessionmanager.ErrMissingHarness, "Invalid", "AGENT_REQUIRED", ""},
		{sessionmanager.ErrArtifactOutputUnavailable, "Invalid", "ARTIFACT_OUTPUT_UNAVAILABLE", "Artifact output needs a daemon data directory to hold the session's artifacts"},
		{sessionmanager.ErrHarnessInstallActive, "Conflict", "HARNESS_INSTALL_ACTIVE", "The selected harness is currently being installed"},
		{sessionmanager.ErrUnsupportedModel, "Invalid", "UNSUPPORTED_MODEL", ""},
		{sessionmanager.ErrTargetAgentUnauthorized, "Invalid", "TARGET_AGENT_UNAUTHORIZED", "The target agent is not authenticated; authenticate it before switching"},
		{sessionmanager.ErrDeliveryUnconfirmed, "Conflict", "SESSION_DELIVERY_UNCONFIRMED", "Message was written to the terminal but the agent did not start a turn; prompt may be unsubmitted"},
		{sessionmanager.ErrScratchBranchUnsupported, "Invalid", "SCRATCH_BRANCH_UNSUPPORTED", ""},
		{ports.ErrWorkspaceBranchCheckedOutElsewhere, "Conflict", "BRANCH_CHECKED_OUT_ELSEWHERE", ""},
		{ports.ErrWorkspaceDefaultBranchUnresolved, "Invalid", "DEFAULT_BRANCH_UNRESOLVED", ""},
		{ports.ErrWorkspaceBranchNotFetched, "Invalid", "BRANCH_NOT_FETCHED", ""},
		{ports.ErrWorkspaceBranchInvalid, "Invalid", "INVALID_BRANCH", ""},
		{ports.ErrAgentBinaryNotFound, "Invalid", "AGENT_BINARY_NOT_FOUND", ""},
		{ports.ErrRuntimePrerequisite, "Invalid", "RUNTIME_PREREQUISITE_MISSING", ""},
		{ports.ErrRuntimeCommandLineTooLong, "Invalid", "WINDOWS_COMMAND_LINE_TOO_LONG", "The agent launch command exceeds the Windows size limit. Shorten the task or project instructions."},
		{ports.ErrChatDriverUnavailable, "Conflict", "CHAT_DRIVER_UNAVAILABLE", ""},
		{ports.ErrChatDriverIncompatible, "Conflict", "CHAT_DRIVER_INCOMPATIBLE", ""},
		{ports.ErrChatAuthRequired, "Conflict", "CHAT_AUTH_REQUIRED", "The agent is installed but not authenticated"},
		{ports.ErrChatResumeFailed, "Conflict", "CHAT_RESUME_FAILED", "The agent could not resume its saved conversation. Your Tallpine history is still available."},
		{ports.ErrAgentAuthRequired, "Conflict", "AGENT_AUTH_REQUIRED", "The agent is installed but the project credential was rejected"},
		{ports.ErrUnsupportedEffort, "Invalid", "UNSUPPORTED_EFFORT", ""},
		{ports.ErrModelCapabilitiesUnavailable, "Invalid", "MODEL_CAPABILITIES_UNAVAILABLE", ""},
		{ports.ErrRuntimeWorkspaceCwdMismatch, "Conflict", "WORKSPACE_CWD_MISMATCH", ""},
		{ports.ErrWorkspaceLocked, "Conflict", "WORKSPACE_LOCKED", ""},
		{sessionmanager.ErrCleanupScript, "Conflict", "WORKSPACE_CLEANUP_FAILED", "Workspace cleanup script failed; the worktree was preserved. Fix the script and retry cleanup."},
	}
}

func equalAPIError(t *testing.T, got error, want *apierr.Error) {
	t.Helper()
	var gotAPI *apierr.Error
	if !errors.As(got, &gotAPI) {
		t.Fatalf("error %T %v is not an *apierr.Error", got, got)
	}
	if gotAPI.Kind != want.Kind || gotAPI.Code != want.Code || gotAPI.Message != want.Message || len(gotAPI.Details) != len(want.Details) {
		t.Fatalf("got {%v %s %q %v}, want {%v %s %q %v}", gotAPI.Kind, gotAPI.Code, gotAPI.Message, gotAPI.Details, want.Kind, want.Code, want.Message, want.Details)
	}
}

// Every sentinel keeps its API code, kind and message, bare or wrapped.
func TestMapSessionErrorTable(t *testing.T) {
	t.Parallel()
	for _, row := range sessionErrorRows() {
		t.Run(row.code, func(t *testing.T) {
			t.Parallel()
			equalAPIError(t, mapSessionError(row.sentinel), row.want(row.sentinel))
			wrapped := fmt.Errorf("while testing: %w", row.sentinel)
			equalAPIError(t, mapSessionError(wrapped), row.want(wrapped))
		})
	}
}

// An error wrapping two sentinels maps to whichever the table lists first.
func TestMapSessionErrorFirstMatchWins(t *testing.T) {
	t.Parallel()
	rows := sessionErrorRows()
	for i := range rows {
		for j := i + 1; j < len(rows); j++ {
			joined := errors.Join(rows[j].sentinel, rows[i].sentinel)
			var got *apierr.Error
			if !errors.As(mapSessionError(joined), &got) || got.Code != rows[i].code {
				t.Fatalf("join(%s,%s) mapped to %v, want %s", rows[j].code, rows[i].code, got, rows[i].code)
			}
		}
	}
}

func TestMapSessionErrorSpecialCases(t *testing.T) {
	t.Parallel()
	if err := mapSessionError(nil); err != nil {
		t.Fatal("nil must map to nil")
	}
	plain := errors.New("boom")
	if !errors.Is(mapSessionError(plain), plain) {
		t.Fatal("an unknown error must pass through unchanged")
	}
	refused := fmt.Errorf("delete mer-1: %w", &sessionmanager.CleanupRefusedError{Reason: "workspace has uncommitted changes"})
	equalAPIError(t, mapSessionError(refused),
		apierr.Conflict("WORKSPACE_CLEANUP_REFUSED", "The worktree was kept, so the session was not deleted: workspace has uncommitted changes", nil))
	split := &ports.SplitHistoryError{}
	equalAPIError(t, mapSessionError(fmt.Errorf("x: %w", errors.Join(ports.ErrAgentAccountSplitHistory, split))),
		apierr.Conflict("AGENT_ACCOUNT_SPLIT_HISTORY", split.Error(), nil))
	equalAPIError(t, mapSessionError(ports.ErrAgentAccountSplitHistory),
		apierr.Conflict("AGENT_ACCOUNT_SPLIT_HISTORY", ports.ErrAgentAccountSplitHistory.Error(), nil))
	equalAPIError(t, mapSessionError(ports.ErrChatUnsupported),
		apierr.Conflict("SESSION_MODE_UNSUPPORTED", ports.ErrChatUnsupported.Error(), nil))
	recovery := fmt.Errorf("probe: %w", ports.ErrChatRecoveryInconclusive)
	equalAPIError(t, mapSessionError(recovery), apierr.Conflict("CHAT_RECOVERY_INCONCLUSIVE",
		"Tallpine could not safely reconnect to the session's still-running chat process: "+recovery.Error(), nil))

	// The three computed rows keep their place in the order: each beats every
	// row listed after it.
	recoveryAndDriver := errors.Join(ports.ErrChatDriverUnavailable, ports.ErrChatRecoveryInconclusive)
	var got *apierr.Error
	if !errors.As(mapSessionError(recoveryAndDriver), &got) || got.Code != "CHAT_RECOVERY_INCONCLUSIVE" {
		t.Fatalf("recovery + driver mapped to %v, want CHAT_RECOVERY_INCONCLUSIVE", got)
	}
	splitAndUnshared := errors.Join(ports.ErrAgentAccountUnshared, ports.ErrAgentAccountSplitHistory)
	if !errors.As(mapSessionError(splitAndUnshared), &got) || got.Code != "AGENT_ACCOUNT_SPLIT_HISTORY" {
		t.Fatalf("split + unshared mapped to %v, want AGENT_ACCOUNT_SPLIT_HISTORY", got)
	}
	unsupportedAndRecovery := errors.Join(ports.ErrChatRecoveryInconclusive, ports.ErrChatUnsupported)
	if !errors.As(mapSessionError(unsupportedAndRecovery), &got) || got.Code != "SESSION_MODE_UNSUPPORTED" {
		t.Fatalf("unsupported + recovery mapped to %v, want SESSION_MODE_UNSUPPORTED", got)
	}
}

func TestMapSessionErrorChatCapabilityDetails(t *testing.T) {
	t.Parallel()
	capErr := &ports.ChatCapabilityError{
		Harness:                "x",
		Missing:                []ports.ChatCapability{"c1", "c2"},
		AllowedPermissionModes: []ports.PermissionMode{"m1"},
	}
	var got *apierr.Error
	if !errors.As(mapSessionError(errors.Join(ports.ErrChatUnsupported, capErr)), &got) {
		t.Fatal("not an api error")
	}
	missing, _ := got.Details["missingCapabilities"].([]string)
	allowed, _ := got.Details["allowedApprovalModes"].([]string)
	if got.Code != "SESSION_MODE_UNSUPPORTED" || len(missing) != 2 || missing[0] != "c1" || len(allowed) != 1 || allowed[0] != "m1" {
		t.Fatalf("details = %#v code %s", got.Details, got.Code)
	}
}
