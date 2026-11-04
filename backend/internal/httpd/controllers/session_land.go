package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/service/localland"
)

// SessionLandService lands an approved session branch that has no PR.
type SessionLandService interface {
	Land(ctx context.Context, id domain.SessionID, req localland.Request) (localland.Result, error)
}

// SessionLandRequest picks the target branch; empty means the project default.
type SessionLandRequest struct {
	Into   string `json:"into,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// SessionLandResponse describes the land.
type SessionLandResponse struct {
	Target           string   `json:"target"`
	OldSha           string   `json:"oldSha"`
	NewSha           string   `json:"newSha"`
	Pushed           bool     `json:"pushed"`
	UpdatedCheckouts []string `json:"updatedCheckouts"`
	AlreadyLanded    bool     `json:"alreadyLanded"`
	DryRun           bool     `json:"dryRun"`
	Notes            []string `json:"notes"`
}

// SessionLandController serves `tallpine session land`.
type SessionLandController struct {
	Svc SessionLandService
}

// Register mounts the route. Agent tokens: orchestrators only (httpd/agent_scope.go).
func (c *SessionLandController) Register(r chi.Router) {
	r.Post("/sessions/{sessionId}/land", c.land)
}

var landErrorCodes = []struct {
	err    error
	status int
	kind   string
	code   string
}{
	{localland.ErrSessionNotFound, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND"},
	{localland.ErrBadTarget, http.StatusBadRequest, "bad_request", "LAND_BAD_TARGET"},
	{localland.ErrNoBranch, http.StatusConflict, "conflict", "LAND_NO_BRANCH"},
	{localland.ErrHasPR, http.StatusConflict, "conflict", "LAND_SESSION_HAS_PR"},
	{localland.ErrNotApproved, http.StatusConflict, "conflict", "LAND_NOT_APPROVED"},
	{localland.ErrConflict, http.StatusConflict, "conflict", "LAND_CONFLICT"},
	{localland.ErrDirtyCheckout, http.StatusConflict, "conflict", "LAND_DIRTY_CHECKOUT"},
	{localland.ErrTargetMoved, http.StatusConflict, "conflict", "LAND_TARGET_MOVED"},
	{localland.ErrPushRejected, http.StatusConflict, "conflict", "LAND_PUSH_REJECTED"},
}

func (c *SessionLandController) land(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/land")
		return
	}
	var req SessionLandRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	res, err := c.Svc.Land(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), localland.Request{Into: req.Into, DryRun: req.DryRun})
	if err == nil {
		envelope.WriteJSON(w, http.StatusOK, SessionLandResponse{
			Target: res.Target, OldSha: res.OldSHA, NewSha: res.NewSHA, Pushed: res.Pushed,
			UpdatedCheckouts: nonNil(res.UpdatedCheckouts), AlreadyLanded: res.AlreadyLanded, DryRun: res.DryRun, Notes: nonNil(res.Notes),
		})
		return
	}
	for _, m := range landErrorCodes {
		if errors.Is(err, m.err) {
			envelope.WriteAPIError(w, r, m.status, m.kind, m.code, err.Error(), nil)
			return
		}
	}
	envelope.WriteError(w, r, err)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
