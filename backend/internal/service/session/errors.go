package session

import (
	"errors"
	"fmt"
	"time"

	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
)

type sessionErrorKind int

const (
	kindNotFound sessionErrorKind = iota
	kindInvalid
	kindConflict
	kindForbidden
	kindUnavailable
)

// sessionErrorRule maps one sentinel to its API error. An empty message means
// the wrapped error's own text. A rule with build set computes the whole error.
type sessionErrorRule struct {
	sentinel error
	kind     sessionErrorKind
	code     string
	message  string
	build    func(err error) error
}

func (r sessionErrorRule) apply(err error) error {
	if r.build != nil {
		return r.build(err)
	}
	message := r.message
	if message == "" {
		message = err.Error()
	}
	switch r.kind {
	case kindNotFound:
		return apierr.NotFound(r.code, message)
	case kindInvalid:
		return apierr.Invalid(r.code, message, nil)
	case kindForbidden:
		return apierr.Forbidden(r.code, message)
	case kindUnavailable:
		return apierr.Unavailable(r.code, message)
	default:
		return apierr.Conflict(r.code, message, nil)
	}
}

// toAPIError maps the session engine's sentinel errors to their REST API
// equivalents; an unrecognized error passes through and surfaces as a 500.
func toAPIError(err error) error {
	return mapSessionError(err)
}

// mapSessionError returns the first rule in sessionErrorRules whose sentinel err
// wraps, so the order of the table is the precedence.
func mapSessionError(err error) error {
	if err == nil {
		return nil
	}
	for _, rule := range sessionErrorRules {
		if errors.Is(err, rule.sentinel) {
			return rule.apply(err)
		}
	}
	return err
}

func splitHistoryError(err error) error {
	var split *ports.SplitHistoryError
	message := ports.ErrAgentAccountSplitHistory.Error()
	if errors.As(err, &split) {
		message = split.Error()
	}
	return apierr.Conflict("AGENT_ACCOUNT_SPLIT_HISTORY", message, nil)
}

func cleanupRefusedError(err error) error {
	message := "The worktree was kept, so the session was not deleted"
	var refused *sessionmanager.CleanupRefusedError
	if errors.As(err, &refused) && refused.Reason != "" {
		message += ": " + refused.Reason
	}
	return apierr.Conflict("WORKSPACE_CLEANUP_REFUSED", message, nil)
}

// operationInProgressError names the holding operation and its age (B541) so
// callers know what they are waiting for instead of blind-retrying.
func operationInProgressError(err error) error {
	const message = "Another operation is already running on this session"
	var busy *sessionmanager.OperationInProgressError
	if !errors.As(err, &busy) || busy.Holder == "" {
		return apierr.Conflict("SESSION_OPERATION_IN_PROGRESS", message, nil)
	}
	return apierr.Conflict("SESSION_OPERATION_IN_PROGRESS",
		fmt.Sprintf("%s: %s, running for %s; retry when it finishes", message, busy.Holder, busy.HeldFor.Round(time.Second)),
		map[string]any{"operation": busy.Holder, "heldForSeconds": int(busy.HeldFor.Seconds())})
}

func chatUnsupportedError(err error) error {
	var capabilityErr *ports.ChatCapabilityError
	if errors.As(err, &capabilityErr) {
		missing := make([]string, 0, len(capabilityErr.Missing))
		for _, capability := range capabilityErr.Missing {
			missing = append(missing, string(capability))
		}
		allowed := make([]string, 0, len(capabilityErr.AllowedPermissionModes))
		for _, mode := range capabilityErr.AllowedPermissionModes {
			allowed = append(allowed, string(mode))
		}
		return apierr.Conflict("SESSION_MODE_UNSUPPORTED", err.Error(), map[string]any{
			"missingCapabilities":  missing,
			"allowedApprovalModes": allowed,
		})
	}
	return apierr.Conflict("SESSION_MODE_UNSUPPORTED", err.Error(), nil)
}

func chatRecoveryInconclusiveError(err error) error {
	return apierr.Conflict("CHAT_RECOVERY_INCONCLUSIVE",
		"Tallpine could not safely reconnect to the session's still-running chat process: "+err.Error(), nil)
}

// sessionErrorRules is ordered: the first matching sentinel wins.
var sessionErrorRules = []sessionErrorRule{
	{agentpolicy.ErrRequiresFence, kindConflict, "", "", fenceRequiredError},
	{ports.ErrProjectEnvLocked, kindConflict, ports.CodeProjectEnvLocked, ports.ProjectEnvLockedMessage, nil},
	{sessionmanager.ErrNotFound, kindNotFound, "SESSION_NOT_FOUND", "Unknown session", nil},
	{sessionmanager.ErrSessionNotTerminated, kindConflict, "SESSION_NOT_TERMINATED", "Archive the session before deleting it.", nil},
	{sessionmanager.ErrCleanupRefused, kindConflict, "", "", cleanupRefusedError},
	{sessionmanager.ErrWorkspaceNotReleased, kindConflict, "WORKSPACE_NOT_RELEASED", "The session's worktree has not been cleaned up yet. Run `tallpine session cleanup` first, or delete with cleanup.", nil},
	{domain.ErrSessionHistoryShared, kindConflict, "SESSION_HISTORY_SHARED", "This session's turns belong to a conversation that outlives it, so it cannot be deleted.", nil},
	{ErrPRNotOpen, kindConflict, "PR_NOT_OPEN", "PR is not open", nil},
	{ports.ErrAgentAccountUnknown, kindNotFound, "AGENT_ACCOUNT_NOT_FOUND", "Unknown agent account", nil},
	{ports.ErrAgentAccountProviderMismatch, kindInvalid, "AGENT_ACCOUNT_PROVIDER_MISMATCH", "The account is for a different agent", nil},
	{ports.ErrAgentAccountDefaultUnusable, kindConflict, "AGENT_ACCOUNT_DEFAULT_UNUSABLE", "The default account is signed out or now holds another login, and no other account is signed in. Sign one in again in Settings.", nil},
	{ports.ErrAgentAccountSignedOut, kindConflict, "AGENT_ACCOUNT_SIGNED_OUT", "The account is signed out; sign it in again first", nil},
	{ports.ErrAgentAccountUnchanged, kindConflict, "AGENT_ACCOUNT_UNCHANGED", "The session already uses this account", nil},
	{ports.ErrAgentAccountSplitHistory, kindConflict, "", "", splitHistoryError},
	{ports.ErrAgentAccountUnshared, kindConflict, "AGENT_ACCOUNT_UNSHARED", "Tallpine cannot link this account's folder to this computer's history. On Windows, turn on Developer Mode.", nil},
	{sessionmanager.ErrNotRestorable, kindConflict, "SESSION_NOT_RESTORABLE", "Session is not restorable", nil},
	{sessionmanager.ErrTerminated, kindConflict, "SESSION_TERMINATED", "Session is terminated", nil},
	{sessionmanager.ErrResumeModelDenied, kindForbidden, "MODEL_DENIED", "", nil},
	{sessionmanager.ErrResumeFallbackNotSaved, kindUnavailable, "MODEL_FALLBACK_NOT_SAVED", "", nil},
	{sessionmanager.ErrResumeModelPolicyUnavailable, kindUnavailable, "MODEL_POLICY_UNAVAILABLE", "", nil},
	{sessionmanager.ErrAgentStoppedByUser, kindConflict, "AGENT_STOPPED", "The agent was stopped; resume the session before sending it messages", nil},
	{sessionmanager.ErrAgentExited, kindConflict, "AGENT_EXITED", "The agent process exited; relaunch it before sending another message", nil},
	{sessionmanager.ErrAgentNotExited, kindConflict, "AGENT_NOT_EXITED", "The agent is still running; only exited agents can be resumed", nil},
	{sessionmanager.ErrResumeInProgress, kindConflict, "AGENT_RESUME_IN_PROGRESS", "The agent is already being resumed", nil},
	{sessionmanager.ErrAgentExitInProgress, kindConflict, "AGENT_EXIT_IN_PROGRESS", "The agent is already exiting", nil},
	{sessionmanager.ErrOperationInProgress, kindConflict, "", "", operationInProgressError},
	{sessionmanager.ErrInterfaceTransitionInProgress, kindConflict, "INTERFACE_TRANSITION_IN_PROGRESS", "This session is already switching interfaces", nil},
	{sessionmanager.ErrInterfaceHandoffUnsupported, kindConflict, "INTERFACE_HANDOFF_UNSUPPORTED", "", nil},
	{sessionmanager.ErrNativeConversationMissing, kindConflict, "NATIVE_SESSION_MISSING", "The agent has not exposed a native conversation that can resume in the other interface", nil},
	{sessionmanager.ErrNativeConversationUnverified, kindConflict, "NATIVE_SESSION_UNVERIFIED", "The current terminal launch has not confirmed that it owns the stored native conversation yet", nil},
	{sessionmanager.ErrInterfaceWorkspaceUnavailable, kindConflict, "WORKSPACE_UNAVAILABLE", "The session's workspace was never set up; retry its start before switching interfaces", nil},
	{sessionmanager.ErrInterfaceTransitionNotCancellable, kindConflict, "INTERFACE_TRANSITION_NOT_CANCELLABLE", "The source controller has already stopped; Tallpine must finish or recover the switch", nil},
	{sessionmanager.ErrInterfaceTransitionNoticeNotAcknowledgeable, kindConflict, "INTERFACE_TRANSITION_NOTICE_NOT_ACKNOWLEDGEABLE", "This interface switch has no failure or recovery notice to acknowledge", nil},
	{sessionmanager.ErrInterfaceProviderHistoryRecoveryUnavailable, kindConflict, "PROVIDER_HISTORY_RECOVERY_UNAVAILABLE", "Provider history can be used only after Tallpine identifies a legacy text-only mismatch", nil},
	{sessionmanager.ErrInterfaceAlreadySelected, kindConflict, "INTERFACE_ALREADY_SELECTED", "The session is already using the requested interface", nil},
	{sessionmanager.ErrInterfaceTransitionNotFound, kindNotFound, "INTERFACE_TRANSITION_NOT_FOUND", "Interface switch not found", nil},
	{sessionmanager.ErrAwaitingDecision, kindConflict, "SESSION_AWAITING_DECISION", "Session is paused on a permission decision; answer it in the session terminal first", nil},
	{sessionmanager.ErrComposerBusy, kindConflict, "SESSION_COMPOSER_BUSY", "Session composer has an unsent draft; retry after the draft is submitted or cleared", nil},
	{sessionmanager.ErrStartupPending, kindConflict, "SESSION_STARTUP_PENDING", "Session agent is still starting; retry after the agent prompt is ready", nil},
	{sessionmanager.ErrIncompleteHandle, kindConflict, "SESSION_INCOMPLETE_HANDLE", "Session is missing runtime or workspace handles", nil},
	{sessionmanager.ErrNotResumable, kindConflict, "SESSION_NOT_RESUMABLE", "This session has no saved agent session or prompt to resume from", nil},
	{sessionmanager.ErrProjectNotResolvable, kindInvalid, "PROJECT_NOT_RESOLVABLE", "Project is not registered or has no repo. Register it with `tallpine project add`", nil},
	{sessionmanager.ErrUnknownHarness, kindInvalid, "UNKNOWN_HARNESS", "", nil},
	{sessionmanager.ErrUnsupportedHarness, kindConflict, "HARNESS_UNSUPPORTED", "", nil},
	{sessionmanager.ErrMissingHarness, kindInvalid, "AGENT_REQUIRED", "", nil},
	{sessionmanager.ErrArtifactOutputUnavailable, kindInvalid, "ARTIFACT_OUTPUT_UNAVAILABLE", "Artifact output needs a daemon data directory to hold the session's artifacts", nil},
	{sessionmanager.ErrHarnessInstallActive, kindConflict, "HARNESS_INSTALL_ACTIVE", "The selected harness is currently being installed", nil},
	{sessionmanager.ErrUnsupportedModel, kindInvalid, "UNSUPPORTED_MODEL", "", nil},
	{sessionmanager.ErrTargetAgentUnauthorized, kindInvalid, "TARGET_AGENT_UNAUTHORIZED", "The target agent is not authenticated; authenticate it before switching", nil},
	{sessionmanager.ErrDeliveryUnconfirmed, kindConflict, "SESSION_DELIVERY_UNCONFIRMED", "Message was written to the terminal but the agent did not start a turn; prompt may be unsubmitted", nil},
	{sessionmanager.ErrScratchBranchUnsupported, kindInvalid, "SCRATCH_BRANCH_UNSUPPORTED", "", nil},
	{ports.ErrWorkspaceBranchCheckedOutElsewhere, kindConflict, "BRANCH_CHECKED_OUT_ELSEWHERE", "", nil},
	{ports.ErrWorkspaceDefaultBranchUnresolved, kindInvalid, "DEFAULT_BRANCH_UNRESOLVED", "", nil},
	{ports.ErrWorkspaceBranchNotFetched, kindInvalid, "BRANCH_NOT_FETCHED", "", nil},
	{ports.ErrWorkspaceBranchInvalid, kindInvalid, "INVALID_BRANCH", "", nil},
	{ports.ErrAgentBinaryNotFound, kindInvalid, "AGENT_BINARY_NOT_FOUND", "", nil},
	{ports.ErrRuntimePrerequisite, kindInvalid, "RUNTIME_PREREQUISITE_MISSING", "", nil},
	{ports.ErrRuntimeCommandLineTooLong, kindInvalid, "WINDOWS_COMMAND_LINE_TOO_LONG", "The agent launch command exceeds the Windows size limit. Shorten the task or project instructions.", nil},
	{ports.ErrChatUnsupported, kindConflict, "", "", chatUnsupportedError},
	// Checked before the envelope's transient mapping: a recovery that cannot safely take
	// over a still-running provider fails the same way on every retry, often with a
	// deadline in the chain, so it is not "momentarily unavailable".
	{ports.ErrChatRecoveryInconclusive, kindConflict, "", "", chatRecoveryInconclusiveError},
	{ports.ErrChatDriverUnavailable, kindConflict, "CHAT_DRIVER_UNAVAILABLE", "", nil},
	{ports.ErrChatDriverIncompatible, kindConflict, "CHAT_DRIVER_INCOMPATIBLE", "", nil},
	{ports.ErrChatAuthRequired, kindConflict, "CHAT_AUTH_REQUIRED", "The agent is installed but not authenticated", nil},
	{ports.ErrChatResumeFailed, kindConflict, "CHAT_RESUME_FAILED", "The agent could not resume its saved conversation. Your Tallpine history is still available.", nil},
	{ports.ErrAgentAuthRequired, kindConflict, "AGENT_AUTH_REQUIRED", "The agent is installed but the project credential was rejected", nil},
	{ports.ErrUnsupportedEffort, kindInvalid, "UNSUPPORTED_EFFORT", "", nil},
	{ports.ErrModelCapabilitiesUnavailable, kindInvalid, "MODEL_CAPABILITIES_UNAVAILABLE", "", nil},
	{ports.ErrRuntimeWorkspaceCwdMismatch, kindConflict, "WORKSPACE_CWD_MISMATCH", "", nil},
	{ports.ErrWorkspaceLocked, kindConflict, "WORKSPACE_LOCKED", "", nil},
	{sessionmanager.ErrCleanupScript, kindConflict, "WORKSPACE_CLEANUP_FAILED", "Workspace cleanup script failed; the worktree was preserved. Fix the script and retry cleanup.", nil},
}

// fenceRequiredError is the hardened profile's refusal of a launch it cannot
// fence (409 POLICY_REQUIRES_FENCE), naming the harness and OS.
func fenceRequiredError(err error) error {
	var fence *agentpolicy.FenceRequiredError
	if !errors.As(err, &fence) {
		return apierr.Conflict(agentpolicy.CodeRequiresFence, err.Error(), nil)
	}
	return apierr.Conflict(agentpolicy.CodeRequiresFence, fence.Error(), map[string]any{"harness": fence.Harness, "os": fence.GOOS})
}
