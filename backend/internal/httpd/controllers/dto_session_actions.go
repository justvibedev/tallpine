package controllers

import (
	"encoding/json"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SetSessionReviewerRequest sets the durable reviewer preference for a session.
// Empty clears the preference and falls back to project configuration.
type SetSessionReviewerRequest struct {
	Harness     domain.ReviewerHarness `json:"harness,omitempty" enum:"claude-code,codex,kiro,deepseek-harness"`
	AgentConfig domain.AgentConfig     `json:"agentConfig,omitempty"`
}

// SetSessionAutoReviewRequest configures daemon-side review automation.
type SetSessionAutoReviewRequest struct {
	Enabled        bool `json:"enabled"`
	enabledPresent bool
}

// UnmarshalJSON distinguishes an omitted required boolean from an explicit
// false without making the generated API schema nullable.
func (r *SetSessionAutoReviewRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.enabledPresent = wire.Enabled != nil
	if wire.Enabled != nil {
		r.Enabled = *wire.Enabled
	}
	return nil
}

// SetSessionPreviewRequest is the body of POST /api/v1/sessions/{sessionId}/preview.
// An empty url asks the daemon to autodetect a static entry point in the
// session workspace; a non-empty url is resolved as a workspace file when
// possible and otherwise retained as an external target.
type SetSessionPreviewRequest struct {
	URL                  string `json:"url,omitempty" description:"Preview target URL. When empty, the daemon autodetects a static entry point in the session workspace."`
	RequireWorkspaceFile bool   `json:"requireWorkspaceFile,omitempty" description:"Reject the target unless it resolves to an existing file in the session workspace."`
}

// StartPreviewServerRequest selects one named entry from .tallpine/launch.json. The
// name may be omitted when the file contains exactly one configuration.
type StartPreviewServerRequest struct {
	Configuration string `json:"configuration,omitempty" description:"Named preview configuration. Optional when exactly one configuration exists."`
}

// BrowserStatusQuery selects the session whose logical browser is inspected.
type BrowserStatusQuery struct {
	SessionID domain.SessionID `query:"sessionId" description:"Tallpine session identifier."`
}

// BrowserCapabilityHeader proves that the caller owns the target session.
type BrowserCapabilityHeader struct {
	Capability string `header:"X-AO-Browser-Capability" description:"Opaque browser capability injected into the owning Tallpine worker."`
}

// PreviewCapabilityHeader lets a session-scoped user shell manage only its
// own preview server, without granting browser automation access.
type PreviewCapabilityHeader struct {
	Capability string `header:"X-AO-Preview-Capability" description:"Opaque preview-only capability injected into a session-scoped user shell."`
}

// SetSessionMergePolicyRequest is the body of PATCH /api/v1/sessions/{sessionId}/merge-policy.
type SetSessionMergePolicyRequest struct {
	TerminateOnPRMerge bool `json:"terminateOnPrMerge"`
}

// SetSessionMergePolicyResponse is the body of PATCH /api/v1/sessions/{sessionId}/merge-policy.
type SetSessionMergePolicyResponse struct {
	OK                 bool             `json:"ok"`
	SessionID          domain.SessionID `json:"sessionId"`
	TerminateOnPRMerge bool             `json:"terminateOnPrMerge"`
	Session            SessionView      `json:"session"`
}

// SetSessionAutoInjectReviewRequest is the body of PATCH /api/v1/sessions/{sessionId}/auto-inject-review.
type SetSessionAutoInjectReviewRequest struct {
	AutoInjectReview bool `json:"autoInjectReview"`
}

// SetSessionAutoInjectReviewResponse is the response from updating a session's automatic review-injection policy.
type SetSessionAutoInjectReviewResponse struct {
	OK               bool             `json:"ok"`
	SessionID        domain.SessionID `json:"sessionId"`
	AutoInjectReview bool             `json:"autoInjectReview"`
	Session          SessionView      `json:"session"`
}

// SetSessionAutoInjectCIRequest updates automatic CI delivery for a session
// and every PR currently owned by it.
type SetSessionAutoInjectCIRequest struct {
	AutoInjectCI bool `json:"autoInjectCI"`
}

// SetSessionAutoInjectCIResponse confirms the persisted session policy.
type SetSessionAutoInjectCIResponse struct {
	OK           bool             `json:"ok"`
	SessionID    domain.SessionID `json:"sessionId"`
	AutoInjectCI bool             `json:"autoInjectCI"`
	Session      SessionView      `json:"session"`
}

// StartSessionInterfaceTransitionRequest is the body of POST
// /api/v1/sessions/{sessionId}/interface-transition.
type StartSessionInterfaceTransitionRequest struct {
	TargetMode    domain.SessionMode                             `json:"targetMode" enum:"chat,tui"`
	Policy        domain.SessionInterfaceTransitionPolicy        `json:"policy" enum:"drain,interrupt"`
	HistoryPolicy domain.SessionInterfaceTransitionHistoryPolicy `json:"historyPolicy,omitempty" enum:"strict,provider_history"`
}

// SessionInterfaceTransitionView is the client-facing progress record. The
// provider-native conversation id is intentionally not exposed: clients need
// controller state, not an adapter implementation detail.
type SessionInterfaceTransitionView struct {
	ID            string                                         `json:"id"`
	SessionID     domain.SessionID                               `json:"sessionId"`
	SourceMode    domain.SessionMode                             `json:"sourceMode" enum:"chat,tui"`
	TargetMode    domain.SessionMode                             `json:"targetMode" enum:"chat,tui"`
	Policy        domain.SessionInterfaceTransitionPolicy        `json:"policy" enum:"drain,interrupt"`
	HistoryPolicy domain.SessionInterfaceTransitionHistoryPolicy `json:"historyPolicy" enum:"strict,provider_history"`
	Phase         domain.SessionInterfaceTransitionPhase         `json:"phase" enum:"requested,preflighting,draining,source_stopping,source_stopped,target_starting,activating,completed,failed,cancelled,recovery_required"`
	// SourceAccountID and TargetAccountID are set when the handoff moves the
	// agent to another provider account; the mode then stays the same.
	SourceAccountID      string     `json:"sourceAccountId,omitempty"`
	TargetAccountID      string     `json:"targetAccountId,omitempty"`
	ErrorCode            string     `json:"errorCode,omitempty"`
	ErrorDetail          string     `json:"errorDetail,omitempty"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
	CompletedAt          *time.Time `json:"completedAt,omitempty"`
	NoticeAcknowledgedAt *time.Time `json:"noticeAcknowledgedAt,omitempty"`
}

// SessionInterfaceTransitionStatusResponse is the body of GET
// /api/v1/sessions/{sessionId}/interface-transition.
type SessionInterfaceTransitionStatusResponse struct {
	Supported  bool                            `json:"supported"`
	TargetMode domain.SessionMode              `json:"targetMode" enum:"chat,tui"`
	ReasonCode string                          `json:"reasonCode,omitempty"`
	Reason     string                          `json:"reason,omitempty"`
	Transition *SessionInterfaceTransitionView `json:"transition,omitempty"`
}

// StartSessionInterfaceTransitionResponse acknowledges an asynchronous handoff.
type StartSessionInterfaceTransitionResponse struct {
	OK         bool                           `json:"ok"`
	SessionID  domain.SessionID               `json:"sessionId"`
	Transition SessionInterfaceTransitionView `json:"transition"`
}

// CancelSessionInterfaceTransitionResponse acknowledges cancellation.
type CancelSessionInterfaceTransitionResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
}

// InterfaceTransitionNoticeAckResponse returns the retained transition with
// its durable notice acknowledgement.
type InterfaceTransitionNoticeAckResponse struct {
	OK         bool                           `json:"ok"`
	SessionID  domain.SessionID               `json:"sessionId"`
	Transition SessionInterfaceTransitionView `json:"transition"`
}

// KillSessionRequest is the optional body of POST /api/v1/sessions/{sessionId}/kill.
// DeleteArtifacts empties the session's artifact directory once it is
// terminated. Only the archive dialog sets it; the CLI and the reaper never do.
type KillSessionRequest struct {
	DeleteArtifacts bool `json:"deleteArtifacts,omitempty"`
}

// DelegateTaskRequest is the body of POST /api/v1/orchestrators/delegate.
// An omitted agent tells the orchestrator to use the project's worker default.
type DelegateTaskRequest struct {
	ClientRequestID string              `json:"clientRequestId,omitempty" maxLength:"128"`
	ProjectID       domain.ProjectID    `json:"projectId"`
	Brief           string              `json:"brief" maxLength:"16384"`
	Agent           domain.AgentHarness `json:"agent,omitempty" enum:"claude-code,codex,kiro,deepseek-harness,fake"`
	Model           string              `json:"model,omitempty" maxLength:"256"`
	// Effort is an explicit, provider-advertised model tuning override. Nil
	// inherits the project default; an empty string selects the provider default.
	Effort *string `json:"effort,omitempty" maxLength:"64"`
	// ApprovalMode is an optional per-session override. The UI uses the explicit
	// bypass value only after the user accepts an approval-less Chat fallback.
	ApprovalMode domain.PermissionMode `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	// Mode is omitted for the daemon-owned default. The UI sends tui only when
	// the user explicitly accepts the fallback after Chat preflight fails.
	Mode domain.SessionMode `json:"mode,omitempty" enum:"tui,chat"`
	// Attachments are files pasted, dropped, or picked into the delegated task
	// brief. Each carries bytes as standard base64 (no data: URL prefix). The
	// daemon writes them into the spawned worker worktree and appends path
	// references to the worker prompt.
	Attachments []AttachmentInput `json:"attachments,omitempty"`
	// TaskPreparation is the opaque worktree token returned while the New Task
	// dialog is open. Missing or expired tokens fall back to normal creation.
	TaskPreparation string `json:"taskPreparation,omitempty"`
	// BacklogRef starts the worker on a backlog item (B12): the item's title,
	// note, done-when and agent notes join the worker's task and the session is
	// linked to the item, as `tallpine spawn --backlog` does.
	BacklogRef string `json:"backlogRef,omitempty" maxLength:"64"`
	// BacklogForce starts a held backlog item, or one with an open blocker, anyway.
	BacklogForce bool `json:"backlogForce,omitempty"`
	// BacklogAllowDuplicate starts the item although a live session already holds it.
	BacklogAllowDuplicate bool `json:"backlogAllowDuplicate,omitempty"`
	// BacklogAllowBlocked starts the item although a blocker is unfinished, without
	// skipping the plan checks (unlike BacklogForce).
	BacklogAllowBlocked bool `json:"backlogAllowBlocked,omitempty"`
}

// PrepareTaskResponse returns the opaque token for a speculative task workspace.
type PrepareTaskResponse struct {
	OK              bool   `json:"ok"`
	TaskPreparation string `json:"taskPreparation,omitempty"`
}

// DelegateTaskResponse confirms which worker was spawned and, when available,
// which orchestrator received the follow-up title request.
type DelegateTaskResponse struct {
	OK             bool             `json:"ok"`
	WorkerID       domain.SessionID `json:"workerId"`
	OrchestratorID domain.SessionID `json:"orchestratorId,omitempty"`
	// Warnings are non-fatal notices, e.g. a backlog item with open blockers.
	Warnings []string `json:"warnings,omitempty"`
}
