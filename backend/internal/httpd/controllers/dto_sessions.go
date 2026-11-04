package controllers

import "tallpine.eu/tallpine/backend/internal/domain"

// SpawnSessionResponse includes ephemeral measurements of the final assembled
// prompt texts. The fields are required so a measured zero remains distinct
// from a response that never measured prompt sizes.
type SpawnSessionResponse struct {
	Session           SessionView `json:"session"`
	PromptBytes       int         `json:"promptBytes"`
	SystemPromptBytes int         `json:"systemPromptBytes"`
	// Warnings are non-fatal notices, e.g. a backlog item with open blockers.
	Warnings []string `json:"warnings,omitempty"`
}

// StageSessionAttachmentsRequest attaches files to a session that is already
// running, for a caller that will name the returned paths in its next message.
type StageSessionAttachmentsRequest struct {
	// Attachments each carry their bytes as standard base64 (no data: URL prefix).
	// The same count, size, and blocked-type rules as spawn apply.
	Attachments []AttachmentInput `json:"attachments"`
}

// StageSessionAttachmentsResponse is where the files were written.
type StageSessionAttachmentsResponse struct {
	SessionID domain.SessionID `json:"sessionId"`
	// Paths are worktree-relative and forward-slashed, in the order submitted. They
	// are what the agent can actually open, so a client must send these verbatim
	// rather than a display form of them.
	Paths []string `json:"paths"`
}
