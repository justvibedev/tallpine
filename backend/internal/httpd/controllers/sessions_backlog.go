package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// prepareBacklogSpawn validates a request's backlogRef before anything
// launches. With no ref it returns a zero item. On failure it has already
// written the response and ok is false. method/path name the endpoint for the
// 501 answer when no backlog service is wired.
func (c *SessionsController) prepareBacklogSpawn(w http.ResponseWriter, r *http.Request, method, path string, projectID domain.ProjectID, ref string, force, allowDuplicate, allowBlocked bool, arm string) (item backlogsvc.SpawnItem, warnings []string, ok bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return item, nil, true
	}
	if c.Backlog == nil {
		apispec.NotImplemented(w, r, method, path)
		return item, nil, false
	}
	item, err := c.Backlog.PrepareSpawnPlan(backlogsvc.WithSpawner(r.Context(), backlogActor(r)), projectID, ref, force, allowDuplicate, allowBlocked, arm)
	if err != nil {
		envelope.WriteError(w, r, err)
		return backlogsvc.SpawnItem{}, nil, false
	}
	if item.Warning != "" {
		warnings = append(warnings, item.Warning)
	}
	return item, warnings, true
}

// linkBacklogSession links a launched session to its backlog item. A link
// failure never fails the launch; it is logged and appended to warnings.
func (c *SessionsController) linkBacklogSession(r *http.Request, item backlogsvc.SpawnItem, sessionID domain.SessionID, createdBy string, warnings []string) []string {
	if item.ID == "" {
		return warnings
	}
	if err := c.Backlog.LinkSession(r.Context(), item.ID, sessionID, createdBy); err != nil {
		slog.Warn("link session to backlog item failed", "item", item.Ref, "session", sessionID, "error", err)
		warnings = append(warnings, fmt.Sprintf("session %s started but could not be linked to %s: %v", sessionID, item.Ref, err))
	}
	return warnings
}
