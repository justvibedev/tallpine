package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
)

// MergesService builds the cross-lane merge digest.
type MergesService interface {
	Digest(ctx context.Context, project domain.ProjectID) (mergessvc.Digest, error)
}

// MergesProjectIDParam is the {projectId} path parameter of the merges route.
type MergesProjectIDParam struct {
	ProjectID string `path:"projectId" description:"Project whose merge digest to read."`
}

// MergeDigestResponse is the body of GET /api/v1/projects/{projectId}/merges.
type MergeDigestResponse mergessvc.Digest

// MergesController serves the merge digest.
type MergesController struct{ Svc MergesService }

// Register mounts the merges route.
func (c *MergesController) Register(r chi.Router) {
	r.Get("/projects/{projectId}/merges", c.digest)
}

func (c *MergesController) digest(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{projectId}/merges")
		return
	}
	d, err := c.Svc.Digest(r.Context(), domain.ProjectID(chi.URLParam(r, "projectId")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MergeDigestResponse(d))
}
