package controllers

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/workspacewatch"
)

// Not under artifact-files/, where it would shadow a file named "events".
const artifactEventsPath = "/api/v1/sessions/{sessionId}/artifact-events"

// streamArtifactChanges tells a client viewing a session's artifacts that a
// file in its artifact directory changed, so an open view rereads it instead
// of showing what it opened with. Each event is only an invalidation edge: a
// burst of writes coalesces, and the client rereads through the usual routes.
func (c *SessionsController) streamArtifactChanges(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, artifactEventsPath)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SSE_UNSUPPORTED", "Streaming is not supported by this server", nil)
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	dir := sess.Metadata.ArtifactDir
	if info, statErr := os.Stat(dir); dir == "" || statErr != nil || !info.IsDir() {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "ARTIFACT_DIR_NOT_FOUND", "artifact directory not found", nil)
		return
	}
	changes, err := workspacewatch.Watch(r.Context(), dir)
	if err != nil {
		envelope.WriteError(w, r, fmt.Errorf("watch artifact directory: %w", err))
		return
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// The watch cannot report a write made before it existed, so a client
	// rereads once on ready.
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
			if _, err := fmt.Fprint(w, "event: artifacts_changed\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, "event: heartbeat\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
