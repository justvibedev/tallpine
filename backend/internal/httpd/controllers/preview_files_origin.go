package controllers

import (
	"net/http"
	"path"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

// previewFileOriginURL mints a capability-bearing URL that opens one workspace
// file on the workspace files origin. It reads and persists nothing: the
// session's preview entry, revision and origin root are untouched, so opening
// a file from chat never replaces what the agent or `tallpine preview` set.
func (c *SessionsController) previewFileOriginURL(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview/file-url")
		return
	}
	id := sessionID(r)
	if _, err := c.Svc.Get(r.Context(), id); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	clean, ok := previewutil.CleanWorkspacePath(r.URL.Query().Get("path"))
	if !ok || previewutil.IsSecretPath(clean) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	u, err := previewutil.WorkspaceFilesURL("http://"+r.Host, id, clean)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PreviewFileURLResponse{URL: u})
}

// workspaceFilesOrigin serves a workspace file from the workspace root on the
// capability-bearing files origin, whatever the stored preview entry is.
func (c *SessionsController) workspaceFilesOrigin(w http.ResponseWriter, r *http.Request, id domain.SessionID) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
			r.Method+" not allowed on preview origin", nil)
		return
	}
	if c.Svc == nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_NOT_FOUND", "Preview not found", nil)
		return
	}
	sess, err := c.Svc.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	c.serveWorkspacePreviewPath(w, r, sess, strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/"))
}
