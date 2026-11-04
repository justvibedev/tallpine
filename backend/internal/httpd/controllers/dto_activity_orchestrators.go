package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// SetActivityResponse is the body of POST /api/v1/sessions/{sessionId}/activity.
type SetActivityResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	State     string           `json:"state"`
}

// SetReviewActivityResponse is the body of POST /api/v1/reviews/{reviewSessionID}/activity.
type SetReviewActivityResponse struct {
	OK              bool   `json:"ok"`
	ReviewSessionID string `json:"reviewSessionId"`
}

// OrchestratorIDParam is the {id} path parameter for orchestrator routes.
type OrchestratorIDParam struct {
	ID string `path:"id" description:"Orchestrator session identifier, e.g. project-orchestrator."`
}

// ReviewSessionIDParam is the {reviewSessionID} path parameter for reviewer-owned routes.
type ReviewSessionIDParam struct {
	ID string `path:"reviewSessionID" description:"Reviewer session identifier, currently the per-harness review row id."`
}

// ReviewIDParam identifies a durable reviewer-owned conversation.
type ReviewIDParam struct {
	ReviewID string `path:"reviewId" description:"Reviewer conversation identifier."`
}

// SpawnOrchestratorRequest is the body of POST /api/v1/orchestrators.
type SpawnOrchestratorRequest struct {
	ProjectID domain.ProjectID `json:"projectId"`
	Clean     bool             `json:"clean,omitempty"`
	// Mode applies only when this request creates a project orchestrator. An
	// idempotent ensure returns the existing orchestrator unchanged, and a clean
	// replacement inherits the existing orchestrator's currently committed mode.
	Mode domain.SessionMode `json:"mode,omitempty" enum:"chat,tui"`
	// ApprovalMode is an optional per-session override. The UI uses the explicit
	// bypass value only after the user accepts an approval-less Chat fallback.
	ApprovalMode domain.PermissionMode `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	// Lane is the backlog item ref (e.g. "B12") of a top-level topic item this
	// orchestrator owns. Workers it spawns inherit the lane and their reports
	// go to this orchestrator only. Clean replacement and the idempotent
	// "already running" check apply within the lane. Empty is the project's
	// default orchestrator.
	Lane string `json:"lane,omitempty"`
	// Prompt is the new orchestrator's first user message, delivered like a
	// spawn prompt (unframed) once its chat controller is live. When it names a
	// running orchestrator instead, nothing is delivered (promptDelivered false).
	Prompt string `json:"prompt,omitempty"`
	// Model overrides the model of a lane orchestrator (with lane); empty uses
	// the project default.
	Model string `json:"model,omitempty"`
}

// SpawnOrchestratorResponse is the body of POST /api/v1/orchestrators.
type SpawnOrchestratorResponse struct {
	Orchestrator OrchestratorResponse `json:"orchestrator"`
	// PromptDelivered is true when the request's prompt became the new
	// orchestrator's first turn.
	PromptDelivered bool `json:"promptDelivered,omitempty"`
}

// HandoffOrchestratorRequest is the body of POST /api/v1/orchestrators/{id}/handoff.
type HandoffOrchestratorRequest struct {
	// NotePath is the state note, an absolute path inside the orchestrator's
	// artifact directory. Empty uses the newest note in that directory.
	NotePath string `json:"notePath,omitempty"`
}

// OrchestratorResponse is the minimal orchestrator read model returned after spawn.
type OrchestratorResponse struct {
	ID          domain.SessionID `json:"id"`
	ProjectID   domain.ProjectID `json:"projectId"`
	ProjectName string           `json:"projectName,omitempty"`
}
