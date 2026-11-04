package controllers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/officialimport"
	officialimportsvc "tallpine.eu/tallpine/backend/internal/service/officialimport"
)

// AOImportService is the controller-facing Tallpine import contract.
type AOImportService interface {
	Run(ctx context.Context, in officialimportsvc.RunInput) (officialimport.Report, error)
}

// AOImportController serves the official-Agent-Orchestrator migration.
type AOImportController struct {
	Svc AOImportService
}

// Register mounts the import route on the supplied router.
func (c *AOImportController) Register(r chi.Router) {
	r.Post("/imports/ao", c.run)
}

func (c *AOImportController) run(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/imports/ao")
		return
	}
	var req AOImportRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "request body must be valid JSON", nil)
		return
	}
	rep, err := c.Svc.Run(r.Context(), officialimportsvc.RunInput{
		SourceDataDir:   req.SourceDataDir,
		DryRun:          req.DryRun,
		ProjectIDs:      req.ProjectIDs,
		IncludeSettings: req.IncludeSettings,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AOImportResponse{Report: rep})
}
