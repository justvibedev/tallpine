package controllers

import (
	"context"
	"errors"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// ClearedContextNoter builds the note an agent reads after its context was
// cleared (chat.Service.ClearedContextNote).
type ClearedContextNoter interface {
	ClearedContextNote(ctx context.Context, id domain.SessionID) (string, error)
}

// clearedContextNote serves the note a Terminal UI agent's SessionStart hook
// adds to a context the user cleared with /clear, so the agent gets its task
// and conversation back.
func (c *SessionsController) clearedContextNote(w http.ResponseWriter, r *http.Request) {
	if c.ClearedNotes == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/cleared-context-note")
		return
	}
	note, err := c.ClearedNotes.ClearedContextNote(r.Context(), sessionID(r))
	switch {
	case errors.Is(err, ports.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "session not found", nil)
		return
	case err != nil:
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ClearedContextNoteResponse{Note: note})
}
