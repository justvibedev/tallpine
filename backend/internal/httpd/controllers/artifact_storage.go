package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/service/artifactstorage"
)

// ArtifactStorageService is the controller-facing artifact disk usage and
// retention contract.
type ArtifactStorageService interface {
	Report(ctx context.Context, olderThanDays int) (artifactstorage.Report, error)
	Retain(ctx context.Context, action artifactstorage.Action, olderThanDays int, ids []domain.SessionID) ([]artifactstorage.Result, error)
}

// ArtifactStorageController owns the artifact usage report and the
// user-confirmed retention action.
type ArtifactStorageController struct {
	Svc ArtifactStorageService
}

// Register mounts the artifact storage routes.
func (c *ArtifactStorageController) Register(r chi.Router) {
	r.Get("/artifacts/storage", c.report)
	r.Post("/artifacts/storage/retention", c.retain)
}

func (c *ArtifactStorageController) report(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/artifacts/storage")
		return
	}
	days, _ := strconv.Atoi(r.URL.Query().Get("olderThanDays"))
	report, err := c.Svc.Report(r.Context(), days)
	if err != nil {
		writeArtifactStorageError(w, r, err)
		return
	}
	out := ArtifactStorageResponse{
		OlderThanDays: report.OlderThanDays, WarnBytes: report.WarnBytes, TotalBytes: report.TotalBytes,
		VersionsBytes: report.VersionsBytes, EligibleBytes: report.EligibleBytes,
		Sessions: make([]ArtifactStorageSession, 0, len(report.Entries)),
	}
	for _, e := range report.Entries {
		out.Sessions = append(out.Sessions, ArtifactStorageSession{
			SessionID: string(e.SessionID), ProjectID: string(e.ProjectID), DisplayName: e.DisplayName,
			Terminated: e.Terminated, UpdatedAt: e.UpdatedAt.Format(time.RFC3339),
			SizeBytes: e.SizeBytes, VersionsBytes: e.VersionsBytes, Files: e.Files,
			OverWarn: e.OverWarn, Eligible: e.Eligible, Protected: e.Protected,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ArtifactStorageController) retain(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/artifacts/storage/retention")
		return
	}
	var req ArtifactRetentionRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	// The destructive step needs an explicit flag, so a client cannot trigger it
	// by posting an otherwise valid body.
	if !req.Confirm {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"ARTIFACT_RETENTION_UNCONFIRMED", "confirm must be true to archive or delete artifacts", nil)
		return
	}
	ids := make([]domain.SessionID, 0, len(req.SessionIDs))
	for _, id := range req.SessionIDs {
		ids = append(ids, domain.SessionID(id))
	}
	results, err := c.Svc.Retain(r.Context(), artifactstorage.Action(req.Action), req.OlderThanDays, ids)
	if err != nil {
		writeArtifactStorageError(w, r, err)
		return
	}
	out := ArtifactRetentionResponse{Results: make([]ArtifactRetentionResult, 0, len(results))}
	for _, res := range results {
		out.FreedBytes += res.FreedBytes
		out.Results = append(out.Results, ArtifactRetentionResult{
			SessionID: string(res.SessionID), Status: string(res.Status), Reason: res.Reason,
			FreedBytes: res.FreedBytes, ArchivePath: res.ArchivePath,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func writeArtifactStorageError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, artifactstorage.ErrInvalid) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"ARTIFACT_RETENTION_INVALID", err.Error(), nil)
		return
	}
	envelope.WriteError(w, r, err)
}
