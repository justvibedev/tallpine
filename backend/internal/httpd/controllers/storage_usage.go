package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
	"tallpine.eu/tallpine/backend/internal/service/storageusage"
)

// StorageUsageService is the controller-facing Settings > Storage contract.
type StorageUsageService interface {
	Get(force bool) storageusage.Snapshot
	Delete(ctx context.Context, id string) error
	CleanSafe(ctx context.Context, dryRun bool) (cleanupsvc.Report, error)
}

// StorageUsageController serves everything Tallpine and its agents store.
type StorageUsageController struct {
	Svc StorageUsageService
}

// Register mounts the storage routes.
func (c *StorageUsageController) Register(r chi.Router) {
	r.Get("/storage", c.get)
	r.Post("/storage/delete", c.remove)
	r.Post("/storage/cleanup", c.cleanup)
}

func (c *StorageUsageController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/storage")
		return
	}
	snap := c.Svc.Get(r.URL.Query().Get("refresh") == "true")
	out := StorageResponse{
		Refreshing: snap.Refreshing, TotalBytes: snap.TotalBytes, ComputedAt: formatOptionalTime(snap.ComputedAt),
		Entries: make([]StorageEntry, 0, len(snap.Entries)), Processes: make([]StorageProcess, 0, len(snap.Processes)),
	}
	for _, e := range snap.Entries {
		out.Entries = append(out.Entries, StorageEntry{
			ID: e.ID, Category: string(e.Category), Label: e.Label, Path: e.Path, Bytes: e.Bytes, Files: e.Files,
			NewestAt: formatOptionalTime(e.Newest), ProjectID: string(e.Project), SessionID: string(e.Session),
			State: string(e.State), Partial: e.Partial, Protected: e.Protected,
		})
	}
	if g := snap.GoCache; g != nil {
		out.GoCache = &StorageGoCache{Path: g.Path, Bytes: g.Bytes, Partial: g.Partial, CapBytes: g.CapBytes}
	}
	for _, p := range snap.Processes {
		out.Processes = append(out.Processes, StorageProcess{
			PID: p.PID, Command: p.Command, SessionID: string(p.Session), ProjectID: string(p.Project),
			State: string(p.State), StartedAt: formatOptionalTime(p.Started),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *StorageUsageController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/storage/delete")
		return
	}
	var req StorageDeleteRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	// Removal outlives the request: a closed client must not leave a tree half deleted.
	err := c.Svc.Delete(context.WithoutCancel(r.Context()), req.ID)
	var protected storageusage.ErrProtected
	switch {
	case errors.Is(err, storageusage.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "STORAGE_ENTRY_NOT_FOUND", "that item is gone; refresh the list", nil)
	case errors.As(err, &protected):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "STORAGE_ENTRY_PROTECTED", protected.Error(), nil)
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusOK, StorageDeleteResponse{Removed: req.ID})
	}
}

func (c *StorageUsageController) cleanup(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/storage/cleanup")
		return
	}
	var req StorageCleanupRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	report, err := c.Svc.CleanSafe(context.WithoutCancel(r.Context()), req.DryRun)
	switch {
	case errors.Is(err, cleanupsvc.ErrBusy):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CLEANUP_IN_PROGRESS", err.Error(), nil)
	case err != nil:
		envelope.WriteError(w, r, err)
	default:
		envelope.WriteJSON(w, http.StatusOK, cleanupReportDTO(report))
	}
}
