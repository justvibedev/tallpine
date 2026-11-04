package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	updatesvc "tallpine.eu/tallpine/backend/internal/service/update"
)

// UpdateService reports and starts "Update & restart". The routes are absent
// from the agent allowlist (httpd.agentRules), so only the owner's install
// token reaches them: worker and orchestrator tokens get 403.
type UpdateService interface {
	Status(ctx context.Context) (updatesvc.Status, error)
	Start(ctx context.Context, commit string, allowDowngrade bool) (updatesvc.Result, error)
}

// StartUpdateRequest names the CI-passed commit to install.
type StartUpdateRequest struct {
	Commit string `json:"commit"`
	// AllowDowngrade confirms installing a commit older than the installed one.
	AllowDowngrade bool `json:"allowDowngrade,omitempty"`
}

// UpdateController serves the update routes.
type UpdateController struct {
	Svc UpdateService
}

// Register mounts the update routes.
func (c *UpdateController) Register(r chi.Router) {
	r.Get("/update", c.status)
	r.Post("/update", c.start)
}

func updateResultDTO(r *updatesvc.Result) *UpdateResult {
	if r == nil {
		return nil
	}
	v := UpdateResult(*r)
	return &v
}

func (c *UpdateController) status(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/update")
		return
	}
	st, err := c.Svc.Status(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, UpdateStatusResponse{
		InstalledCommit: st.InstalledCommit, LatestCommit: st.LatestCommit, LatestCIPassed: st.LatestCIPassed,
		Candidate: st.Candidate, UpdateAvailable: st.UpdateAvailable, BuildAvailable: st.BuildAvailable,
		Running: st.Running, Last: updateResultDTO(st.Last),
	})
}

func (c *UpdateController) start(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/update")
		return
	}
	var req StartUpdateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Commit == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_BODY", "a commit is required", nil)
		return
	}
	res, err := c.Svc.Start(r.Context(), req.Commit, req.AllowDowngrade)
	switch {
	case errors.Is(err, updatesvc.ErrRunning):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "UPDATE_RUNNING", err.Error(), nil)
	case errors.Is(err, updatesvc.ErrNotCIPassed), errors.Is(err, updatesvc.ErrNotOnMain), errors.Is(err, updatesvc.ErrDowngrade):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "validation", "UPDATE_COMMIT_REFUSED", err.Error(), nil)
	case errors.Is(err, updatesvc.ErrUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "UPDATE_UNAVAILABLE", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "UPDATE_INVALID", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusAccepted, updateResultDTO(&res))
	}
}
