package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// AttachmentInput is one file attached to a spawn, delegate, stage, or send
// request.
type AttachmentInput struct {
	// MimeType is the browser-reported content type (e.g. "image/png"). Used to
	// derive the on-disk file extension. Explicitly blocked types are rejected.
	MimeType string `json:"mimeType,omitempty"`
	// Data is the raw file bytes, standard base64-encoded, without any
	// "data:...;base64," prefix.
	Data string `json:"data"`
}

// SpawnSessionRequest is the body of POST /api/v1/sessions.
type SpawnSessionRequest struct {
	ClientRequestID string `json:"clientRequestId,omitempty" maxLength:"128"`
	// ProjectID is omitted for a standalone worker session.
	ProjectID domain.ProjectID `json:"projectId,omitempty"`
	IssueID   domain.IssueID   `json:"issueId,omitempty"`
	// ParentSessionID is supplied by `tallpine spawn` inside a Tallpine session. The daemon
	// validates it before deriving inherited worker settings.
	ParentSessionID domain.SessionID       `json:"parentSessionId,omitempty"`
	TrackerProvider domain.TrackerProvider `json:"trackerProvider,omitempty" enum:"github,gitlab"`
	Kind            domain.SessionKind     `json:"kind,omitempty" enum:"worker,orchestrator"`
	Harness         domain.AgentHarness    `json:"harness,omitempty" enum:"claude-code,codex,kiro,deepseek-harness"`
	Branch          string                 `json:"branch,omitempty"`
	// BacklogRef starts the session on a backlog item (B12): the item's title,
	// note, done-when and agent notes join the worker's task, and the session is
	// linked to the item. Project sessions only.
	BacklogRef string `json:"backlogRef,omitempty" maxLength:"64"`
	// BacklogForce starts a held backlog item anyway.
	BacklogForce bool `json:"backlogForce,omitempty"`
	// BacklogAllowDuplicate starts the item although a live session already holds it.
	BacklogAllowDuplicate bool `json:"backlogAllowDuplicate,omitempty"`
	// BacklogPlan forces who plans an item that needs a plan (B335): in-place
	// makes the spawned implementer author the plan and wait read-only for
	// approval, separate keeps today's planner flow. Empty follows the project's
	// planInPlace setting. Needs backlogRef.
	BacklogPlan string `json:"backlogPlan,omitempty" enum:"in-place,separate"`
	// Mode picks the conversation controller: chat talks to the agent over a
	// structured connection, tui opens the agent's native terminal interface.
	// Omitted resolves to the daemon-owned preference, which defaults to Chat for
	// new sessions and falls back to TUI when Chat is unavailable. The preference
	// never mutates existing sessions automatically; compatible sessions may later
	// switch through the durable interface-transition endpoint. An unsupported
	// explicit request fails rather than quietly producing the other kind of session.
	Mode domain.SessionMode `json:"mode,omitempty" enum:"chat,tui"`
	// ApprovalMode overrides the project/default policy for this spawn.
	ApprovalMode domain.PermissionMode `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Prompt       string                `json:"prompt,omitempty" maxLength:"16384"`
	// Model is an optional agent model override scoped to this single spawn. Empty
	// keeps the resolved project/role default. The daemon validates that the
	// selected harness can honor the model before launching.
	Model string `json:"model,omitempty" maxLength:"256"`
	// Effort is the optional reasoning level for the selected model.
	Effort string `json:"effort,omitempty" maxLength:"32"`

	// RequestedOutput asks for a specific deliverable. artifact tells the worker to
	// write a file in its artifact directory instead of opening a PR. Omitted is
	// the ordinary task flow.
	RequestedOutput domain.RequestedOutput `json:"requestedOutput,omitempty" enum:"artifact"`

	// Role starts a special worker. planner is the read-only planning session
	// (B205); it implies requestedOutput artifact. Omitted is an ordinary worker.
	Role domain.SessionRole `json:"role,omitempty" enum:"planner"`

	// DisplayName is the sidebar label for the session, capped at 100 characters.
	// `tallpine spawn --name` always sets it; other clients (e.g. the desktop new-task
	// dialog) may omit it and fall back to the session id in the read model.
	DisplayName string `json:"displayName,omitempty" maxLength:"100"`
	// Attachments are files pasted or dropped into the task brief. Each carries
	// its bytes as standard base64 (no data: URL prefix). The daemon writes them
	// into the session worktree and appends path references to the prompt.
	Attachments []AttachmentInput `json:"attachments,omitempty"`
}
