package controllers

import (
	"context"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// Bounds of GET /settings/changes?limit.
const (
	defaultSettingsChangesLimit = 50
	maxSettingsChangesLimit     = 500
)

// SettingsChangesStore reads the settings change log (B202).
type SettingsChangesStore interface {
	ListSettingsChanges(ctx context.Context, project domain.ProjectID, limit int) ([]domain.SettingsChange, error)
}

// SettingsChangesController serves the settings change log.
type SettingsChangesController struct {
	Store SettingsChangesStore
}

// SettingsChangesQuery documents GET /settings/changes.
type SettingsChangesQuery struct {
	Project string `query:"project" description:"Project whose setting changes to list; empty lists app-wide settings."`
	Limit   int    `query:"limit" minimum:"1" maximum:"500" description:"Changes to return, newest first. Defaults to 50."`
}

// SettingsChangesResponse lists setting changes, newest first.
type SettingsChangesResponse struct {
	Changes []SettingsChange `json:"changes"`
}

// Register mounts the change log route.
func (c *SettingsChangesController) Register(r chi.Router) {
	r.Get("/settings/changes", c.list)
}

func (c *SettingsChangesController) list(w http.ResponseWriter, r *http.Request) {
	if c.Store == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/changes")
		return
	}
	limit := defaultSettingsChangesLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxSettingsChangesLimit {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "INVALID_LIMIT",
				"limit must be a whole number from 1 to "+strconv.Itoa(maxSettingsChangesLimit), nil)
			return
		}
		limit = n
	}
	project := domain.ProjectID(r.URL.Query().Get("project"))
	if refuseOtherProject(w, r, string(project)) {
		return
	}
	rows, err := c.Store.ListSettingsChanges(r.Context(), project, limit)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := SettingsChangesResponse{Changes: make([]SettingsChange, 0, len(rows))}
	for _, row := range rows {
		out.Changes = append(out.Changes, SettingsChange{
			ID: row.ID, ChangedAt: row.ChangedAt.UTC(), Actor: row.Actor, ProjectID: string(row.ProjectID),
			Key: row.Key, OldValue: row.OldValue, NewValue: row.NewValue,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}
