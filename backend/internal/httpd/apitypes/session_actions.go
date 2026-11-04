package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// RenameSessionRequest is the body of PATCH /api/v1/sessions/{sessionId}.
type RenameSessionRequest struct {
	DisplayName string `json:"displayName" minLength:"1" maxLength:"100"`
}

// RenameSessionResponse is the body of PATCH /api/v1/sessions/{sessionId}.
type RenameSessionResponse struct {
	OK          bool             `json:"ok"`
	SessionID   domain.SessionID `json:"sessionId"`
	DisplayName string           `json:"displayName"`
}

// RestoreSessionResponse is the body of POST /api/v1/sessions/{sessionId}/restore.
type RestoreSessionResponse struct {
	OK          bool                   `json:"ok"`
	SessionID   domain.SessionID       `json:"sessionId"`
	RestoreMode domain.RestoreModeView `json:"restoreMode" enum:"native,saved_prompt,fresh"`
	Session     SessionView            `json:"session"`
}

// ExitAgentResponse is the body of POST /api/v1/sessions/{sessionId}/exit-agent.
type ExitAgentResponse struct {
	OK        bool             `json:"ok"`
	SessionID domain.SessionID `json:"sessionId"`
	Session   SessionView      `json:"session"`
}

// ResumeAgentResponse is the body of POST /api/v1/sessions/{sessionId}/resume-agent.
type ResumeAgentResponse struct {
	OK         bool                   `json:"ok"`
	SessionID  domain.SessionID       `json:"sessionId"`
	ResumeMode domain.RestoreModeView `json:"resumeMode" enum:"native,saved_prompt,fresh"`
	Session    SessionView            `json:"session"`
}

// KillSessionResponse is the body of POST /api/v1/sessions/{sessionId}/kill.
type KillSessionResponse struct {
	OK               bool             `json:"ok"`
	SessionID        domain.SessionID `json:"sessionId"`
	Freed            bool             `json:"freed,omitempty"`
	CleanupPending   bool             `json:"cleanupPending,omitempty"`
	ArtifactsDeleted bool             `json:"artifactsDeleted,omitempty"`
}

// CleanupSkippedSession is one terminal session whose workspace cleanup
// preserved rather than reclaimed (a dirty worktree is never force-deleted),
// with the user-facing reason.
type CleanupSkippedSession struct {
	SessionID domain.SessionID `json:"sessionId"`
	Reason    string           `json:"reason"`
}

// CleanupSessionsResponse is the body of POST /api/v1/sessions/cleanup.
type CleanupSessionsResponse struct {
	OK bool `json:"ok"`
	// Cleaned lists sessions whose workspace was present and has been released.
	Cleaned []domain.SessionID `json:"cleaned"`
	// AlreadyGone lists sessions whose workspace directory was already missing,
	// so teardown completed without reclaiming anything.
	AlreadyGone []domain.SessionID      `json:"alreadyGone"`
	Skipped     []CleanupSkippedSession `json:"skipped"`
}
