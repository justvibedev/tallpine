package controllers

import (
	"context"
	"net/http"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// DeleteSessionQuery is the query string accepted by DELETE /api/v1/sessions/{sessionId}.
type DeleteSessionQuery struct {
	Cleanup bool `query:"cleanup,omitempty" description:"When true, a worktree that is still on disk is cleaned up first, the way session cleanup does it; a dirty or locked worktree is never forced and refuses the delete."`
}

// DeleteSessionResponse aliases apitypes.DeleteSessionResponse.
type DeleteSessionResponse = apitypes.DeleteSessionResponse

// sessionDeleteService is optional so unrelated controller fakes keep
// implementing the stable SessionService surface.
type sessionDeleteService interface {
	Delete(ctx context.Context, id domain.SessionID, cleanup bool) error
}

// deleteSession permanently removes a terminated session's record. The service
// refuses a live session, and one whose worktree is not cleaned up yet unless
// the caller asks it to clean it up first.
func (c *SessionsController) deleteSession(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(sessionDeleteService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodDelete, "/api/v1/sessions/{sessionId}")
		return
	}
	if err := svc.Delete(r.Context(), sessionID(r), r.URL.Query().Get("cleanup") == "true"); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, DeleteSessionResponse{OK: true, SessionID: sessionID(r)})
}
