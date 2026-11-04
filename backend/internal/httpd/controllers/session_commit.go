package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/service/sessioncommit"
)

// SessionCommitService commits in a session's own worktree.
type SessionCommitService interface {
	Commit(ctx context.Context, session domain.SessionID, message string) (sessioncommit.Result, error)
}

// SessionCommitRequest carries only the message; the git arguments are fixed.
type SessionCommitRequest struct {
	Message string `json:"message"`
}

// SessionCommitResponse names the new commit.
type SessionCommitResponse struct {
	Sha string `json:"sha"`
}

// SessionCommitController serves `tallpine commit`.
type SessionCommitController struct {
	Svc SessionCommitService
}

// Register mounts the route. Agents reach it with their scoped session token,
// for their own session only (see httpd/agent_scope.go).
func (c *SessionCommitController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/commit", c.commit)
}

func (c *SessionCommitController) commit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/commit")
		return
	}
	var req SessionCommitRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	res, err := c.Svc.Commit(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), req.Message)
	switch {
	case err == nil:
		envelope.WriteJSON(w, http.StatusOK, SessionCommitResponse{Sha: res.SHA})
	case errors.Is(err, sessioncommit.ErrSessionNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "No such session", nil)
	case errors.Is(err, sessioncommit.ErrBadMessage):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "COMMIT_MESSAGE_INVALID", "The commit message must be 1 to 16384 bytes", nil)
	case errors.Is(err, sessioncommit.ErrNothingToCommit):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "NOTHING_TO_COMMIT", "There are no changes to commit", nil)
	case errors.Is(err, sessioncommit.ErrNoWorkspace), errors.Is(err, sessioncommit.ErrNotOwnWorktree):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_WORKTREE_UNAVAILABLE", "This session has no worktree of its own to commit in", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}
