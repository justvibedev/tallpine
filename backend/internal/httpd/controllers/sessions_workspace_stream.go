package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/workspacewatch"
)

// branchStateReconciler refreshes a session's persisted branch facts.
type branchStateReconciler interface {
	ReconcileSessionBranchState(ctx context.Context, id domain.SessionID) error
}

func (c *SessionsController) streamWorkspaceChanges(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/workspace/events")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SSE_UNSUPPORTED", "Streaming is not supported by this server", nil)
		return
	}
	paths, err := c.Svc.WorkspaceWatchPaths(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	changes, err := workspacewatch.Watch(r.Context(), paths...)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// The watcher cannot report edits made before it existed. Reconcile both
	// workspace caches after the watch is installed and before telling the client
	// it can stop fallback polling.
	if _, err := c.Svc.ReconcileWorkspaceManifest(r.Context(), sessionID(r)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "event: ready\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()

	keepAlive := time.NewTicker(15 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-changes:
			if !ok {
				return
			}
			c.Svc.InvalidateWorkspaceCache(sessionID(r))
			payload := struct {
				Kind             string `json:"kind"`
				WorkspaceVersion string `json:"workspaceVersion,omitempty"`
				Refreshing       bool   `json:"refreshing"`
				Overflow         bool   `json:"overflow"`
			}{Kind: "dirty", Refreshing: true, Overflow: true}
			data, _ := json.Marshal(payload)
			if _, err := fmt.Fprintf(w, "event: workspace_changed\ndata: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
			if manifest, refreshErr := c.Svc.RefreshWorkspaceManifest(r.Context(), sessionID(r)); refreshErr == nil {
				payload.Kind = "version"
				payload.WorkspaceVersion = manifest.WorkspaceVersion
				payload.Refreshing = false
				data, _ = json.Marshal(payload)
				if _, err := fmt.Fprintf(w, "event: workspace_changed\ndata: %s\n\n", data); err != nil {
					return
				}
				flusher.Flush()
			}
			// The same watch sees commits and pushes. Branch facts refresh after
			// the file list so a commit never delays it, and their change
			// streams to every client as session_updated.
			if reconciler, ok := c.Svc.(branchStateReconciler); ok {
				_ = reconciler.ReconcileSessionBranchState(r.Context(), sessionID(r))
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, "event: heartbeat\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
