package controllers

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

// setPreview persists the browser preview URL the desktop app opens for a
// session and fans out a session_updated CDC event so the dashboard's browser
// panel reacts live. The target is resolved as follows:
//
//   - An empty url opens the workspace's static entry point (index.html and
//     friends), falling back to the session's existing preview target only
//     when no entry point exists.
//   - An explicit workspace-local path (e.g. `index.html`, `./dist/index.html`)
//     is served through the preview/files route so local files load.
//   - Absolute paths and file: URLs are accepted only when their fully resolved
//     target remains inside the fully resolved session workspace. They use the
//     workspace-confined preview origin rather than an automatable file: URL.
//   - External http(s) URLs and host:port dev servers are kept verbatim.
//
// Every call bumps the session's preview revision, so re-running `tallpine preview`
// with the same target still refreshes the panel.
func (c *SessionsController) setPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	var in SetSessionPreviewRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	// Get first so a missing session is rejected with the normal 404 before any
	// write, and so autodetect/local resolution has the workspace path to probe.
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// Passive preview intentionally accepts an explicit external URL or an
	// existing workspace-local file. Arbitrary local files are rejected by every
	// browser surface because the target remains available to session automation.
	// Managed process execution uses the separately capability-protected
	// /preview/server route.
	previewURL := strings.TrimSpace(in.URL)
	if previewURL == "" {
		if entry, ok := discoverPreviewEntry(sess.Metadata.WorkspacePath); ok {
			previewURL, err = previewFileURL(r, sessionID(r), entry)
			if err != nil {
				writePreviewResolveError(w, r, err)
				return
			}
		} else if existing := strings.TrimSpace(sess.Metadata.PreviewURL); existing != "" {
			var resolveErr error
			previewURL, resolveErr = resolvePreviewTarget(r, sessionID(r), sess.Metadata.WorkspacePath, existing, false)
			if resolveErr != nil {
				writePreviewResolveError(w, r, resolveErr)
				return
			}
		} else {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "NO_PREVIEW_ENTRY", "No preview entry point found in session workspace", nil)
			return
		}
	} else {
		var resolveErr error
		previewURL, resolveErr = resolvePreviewTarget(r, sessionID(r), sess.Metadata.WorkspacePath, previewURL, in.RequireWorkspaceFile)
		if resolveErr != nil {
			writePreviewResolveError(w, r, resolveErr)
			return
		}
	}
	updated, err := c.Svc.SetPreview(r.Context(), sessionID(r), previewURL)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, updated)})
}

// clearPreview resets a session's browser preview to empty (`tallpine preview
// clear`). Unlike setPreview with an empty url it never autodetects: it persists
// an empty target so the desktop browser panel returns to its blank state. The
// write still bumps the preview revision, so the panel hears the change over
// CDC even though the url field is now empty.
func (c *SessionsController) clearPreview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	updated, err := c.Svc.SetPreview(r.Context(), sessionID(r), "")
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: c.view(r.Context(), r, updated)})
}

func discoverPreviewEntry(workspacePath string) (string, bool) {
	// Use DiscoverWebEntrypoint (index.html variants only), not DiscoverEntry
	// (which falls back to mostRecentPreviewable — the newest .md/.html in the
	// workspace). Bare `tallpine preview` (no args) hits this path, and agent
	// harnesses run that automatically on new sessions via the using-tallpine skill.
	// With the .md fallback, every new session in a Markdown-rich repo opened
	// its browser panel to an arbitrary repo doc (e.g. test/cli/README.md)
	// instead of staying empty. Mirrors the poller fix from PR #2860.
	// See issue #2859.
	entry, ok := previewutil.DiscoverWebEntrypoint(workspacePath)
	return entry.Path, ok
}

// resolveLocalPreview maps a workspace-local path (e.g. "index.html" or
// "./dist/index.html") to its preview/files proxy URL when the path resolves to
// a regular file inside the session workspace. It returns ok=false for anything
// that already looks like a URL (an http(s)/file scheme, or a host:port dev
// server) and for paths that do not point at a workspace file, so the caller
// keeps those targets verbatim. Explicit parent traversal is rejected by
// resolvePreviewTarget before this function is called.
func resolveLocalPreview(r *http.Request, id domain.SessionID, workspacePath, raw string) (string, bool, error) {
	if raw == "" || hasURLScheme(raw) {
		return "", false, nil
	}
	entry, ok := previewutil.EntryAtPath(workspacePath, raw)
	if !ok {
		return "", false, nil
	}
	resolved, err := previewFileURL(r, id, entry.Path)
	return resolved, true, err
}

func resolvePreviewTarget(r *http.Request, id domain.SessionID, workspacePath, raw string, requireWorkspaceFile bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if filePath, isFileURL, err := previewFileURLPath(raw); isFileURL {
		if err != nil {
			return "", err
		}
		return workspaceAbsolutePreviewURL(r, id, workspacePath, filePath)
	}
	if isAbsolutePreviewPath(raw) {
		return workspaceAbsolutePreviewURL(r, id, workspacePath, raw)
	}
	if !hasURLScheme(raw) && containsParentPathSegment(raw) {
		return "", errPreviewFileOutsideWorkspace
	}
	if resolved, ok, err := resolveLocalPreview(r, id, workspacePath, raw); ok || err != nil {
		return resolved, err
	}
	if requireWorkspaceFile {
		return "", errPreviewFileNotFound
	}
	return raw, nil
}

func containsParentPathSegment(raw string) bool {
	raw = strings.ReplaceAll(raw, `\`, "/")
	for _, segment := range strings.Split(raw, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func isAbsolutePreviewPath(raw string) bool {
	return filepath.IsAbs(raw) || isWindowsAbsolutePath(raw)
}

func isWindowsAbsolutePath(raw string) bool {
	return len(raw) >= 3 && ((raw[0] >= 'a' && raw[0] <= 'z') || (raw[0] >= 'A' && raw[0] <= 'Z')) && raw[1] == ':' && (raw[2] == '\\' || raw[2] == '/')
}

func previewFileURLPath(raw string) (string, bool, error) {
	if len(raw) < len("file:") || !strings.EqualFold(raw[:len("file:")], "file:") {
		return "", false, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", true, errPreviewFileNotFound
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		return "", true, errPreviewFileOutsideWorkspace
	}
	filePath := parsed.Path
	if len(filePath) > 1 && filePath[0] == '/' && isWindowsAbsolutePath(filePath[1:]) {
		filePath = filePath[1:]
	}
	filePath = filepath.FromSlash(filePath)
	if !isAbsolutePreviewPath(filePath) {
		return "", true, errPreviewFileNotFound
	}
	return filePath, true, nil
}

func workspaceAbsolutePreviewURL(
	r *http.Request,
	id domain.SessionID,
	workspacePath,
	filePath string,
) (string, error) {
	workspaceAbs, err := filepath.Abs(workspacePath)
	if err != nil {
		return "", errPreviewFileNotFound
	}
	workspaceResolved, err := filepath.EvalSymlinks(workspaceAbs)
	if err != nil {
		return "", errPreviewFileNotFound
	}
	fileAbs, err := filepath.Abs(filePath)
	if err != nil {
		return "", errPreviewFileNotFound
	}
	fileResolved, err := filepath.EvalSymlinks(fileAbs)
	if err != nil {
		return "", errPreviewFileNotFound
	}
	rel, err := filepath.Rel(workspaceResolved, fileResolved)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errPreviewFileOutsideWorkspace
	}
	entry, ok := previewutil.EntryAtPath(workspaceResolved, filepath.ToSlash(rel))
	if !ok {
		return "", errPreviewFileNotFound
	}
	return previewFileURL(r, id, entry.Path)
}

func writePreviewResolveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errPreviewFileOutsideWorkspace) {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "PREVIEW_FILE_OUTSIDE_WORKSPACE", "Preview files must stay inside the session workspace", nil)
		return
	}
	if errors.Is(err, errPreviewFileNotFound) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	if errors.Is(err, previewutil.ErrPreviewHostUnsupported) {
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PREVIEW_SESSION_ID_UNSUPPORTED", "Session ID is too long for an isolated preview hostname", nil)
		return
	}
	envelope.WriteError(w, r, err)
}

// hasURLScheme reports whether raw begins with an RFC-3986 "scheme:" prefix
// (http:, https:, file:, or a host:port like localhost:5173). It mirrors the
// renderer's withDefaultScheme heuristic so the daemon and browser panel agree
// on what counts as a URL versus a workspace-relative path.
func hasURLScheme(raw string) bool {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == ':' {
			return i > 0
		}
		isSchemeChar := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '.' || c == '-'
		if !isSchemeChar {
			return false
		}
	}
	return false
}

func previewFileURL(r *http.Request, id domain.SessionID, entry string) (string, error) {
	return previewutil.FileURL("http://"+r.Host, id, entry)
}
