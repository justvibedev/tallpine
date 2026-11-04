package controllers

import (
	"net"
	"net/http"
	"path"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

// inlineArtifactOrigin serves the session's artifact directory on its inline
// origin, the one the chat thread frames an HTML artifact from: the request
// path is the artifact path, files only, read-only, in the inline sandbox.
func (c *SessionsController) inlineArtifactOrigin(w http.ResponseWriter, r *http.Request, id domain.SessionID) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
			r.Method+" not allowed on an artifact origin", nil)
		return
	}
	if c.Svc == nil {
		writeArtifactFileNotFound(w, r)
		return
	}
	// The session read fills in the default artifact directory for a row
	// stored without one.
	sess, err := c.Svc.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	asset := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	serveArtifactFile(w, r, inlineArtifactContentSecurityPolicy, sess.Metadata.ArtifactDir, asset)
}

func isPreviewLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// PreviewOrigin serves a workspace preview from its isolated *.localhost
// origin. It returns false when the request host is not a preview origin so the
// daemon router can continue handling its normal API and control surfaces.
//
// The selected entry's directory is mounted at the origin root. For example,
// dist/index.html is reachable at both /dist/ (the persisted URL) and /, while
// /assets/app.css maps to dist/assets/app.css. This mirrors a production static
// server and fixes root-relative URLs without rewriting user-generated files.
func (c *SessionsController) PreviewOrigin(w http.ResponseWriter, r *http.Request) bool {
	// Only hosts presenting the session's capability label are preview
	// origins; an id-only host falls through to the router, which needs the
	// daemon token.
	if id, ok := previewutil.AuthorizedSessionIDFromInlineArtifactHost(r.Host); ok {
		c.inlineArtifactOrigin(w, r, id)
		return true
	}
	if id, ok := previewutil.AuthorizedSessionIDFromFilesHost(r.Host); ok {
		c.workspaceFilesOrigin(w, r, id)
		return true
	}
	id, artifactOrigin := previewutil.AuthorizedSessionIDFromArtifactHost(r.Host)
	if !artifactOrigin {
		var ok bool
		id, ok = previewutil.AuthorizedSessionIDFromHost(r.Host)
		if !ok {
			return false
		}
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		envelope.WriteAPIError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
			r.Method+" not allowed on preview origin", nil)
		return true
	}
	if c.Svc == nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_NOT_FOUND", "Preview not found", nil)
		return true
	}
	sess, err := c.Svc.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return true
	}
	if artifactOrigin {
		// The host alone declares scope on this origin, so the request path
		// is served verbatim from ArtifactDir: there is no marker to strip
		// and nothing a real workspace path could collide with.
		asset := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		c.serveRootedPreviewFile(w, r, sess.Metadata.ArtifactDir, asset)
		return true
	}
	entry, ok := previewOriginEntry(sess, r.URL.Path)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "NO_PREVIEW_ENTRY", "No preview entry point found in session workspace", nil)
		return true
	}
	switch entry.Scope {
	case previewutil.StoredEntryScopeArtifact:
		asset := previewOriginArtifactAssetPath(entry.Path, r.URL.Path)
		c.serveRootedPreviewFile(w, r, sess.Metadata.ArtifactDir, asset)
	default:
		asset := previewOriginAssetPath(entry.Path, r.URL.Path)
		c.serveRootedPreviewFile(w, r, sess.Metadata.WorkspacePath, asset)
	}
	return true
}

func previewOriginEntry(sess domain.Session, requestPath string) (previewutil.StoredEntry, bool) {
	requested := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if rel, ok := previewutil.ArtifactEntryRelative(requested); ok {
		// __ao_artifacts__/ is a reserved marker Tallpine itself prepends when it
		// builds an artifact preview link (see ArtifactEntryPath), but it was
		// a valid workspace-relative path before artifact previews existed.
		// A real workspace file at that exact literal request path must keep
		// winning over the artifact directory, mirroring the same collision
		// guard below for the stored-PreviewURL path and previewFile's guard
		// for the legacy /preview/files route.
		if _, existsInWorkspace := previewutil.EntryAtPath(sess.Metadata.WorkspacePath, requested); existsInWorkspace {
			return previewutil.StoredEntry{Scope: previewutil.StoredEntryScopeWorkspace, Path: requested}, true
		}
		if _, exists := previewutil.EntryAtPath(sess.Metadata.ArtifactDir, rel); exists {
			return previewutil.StoredEntry{Scope: previewutil.StoredEntryScopeArtifact, Path: rel}, true
		}
	}
	if entry, ok := previewutil.StoredEntryFromPreview(sess.Metadata.PreviewURL, sess.ID); ok {
		switch entry.Scope {
		case previewutil.StoredEntryScopeArtifact:
			// __ao_artifacts__/ is a reserved marker Tallpine itself prepends when
			// it builds an artifact preview link, but it was a valid
			// workspace-relative path before artifact previews existed. A
			// real workspace file at that exact literal path must keep
			// winning over the artifact directory, mirroring previewFile's
			// same collision guard for the legacy /preview/files route.
			if literal, ok := previewutil.ArtifactEntryPath(entry.Path); ok {
				if _, existsInWorkspace := previewutil.EntryAtPath(sess.Metadata.WorkspacePath, literal); existsInWorkspace {
					return previewutil.StoredEntry{Scope: previewutil.StoredEntryScopeWorkspace, Path: literal}, true
				}
			}
			if _, exists := previewutil.EntryAtPath(sess.Metadata.ArtifactDir, entry.Path); exists {
				return entry, true
			}
		default:
			if _, exists := previewutil.EntryAtPath(sess.Metadata.WorkspacePath, entry.Path); exists {
				return entry, true
			}
		}
	}
	if entry, ok := discoverPreviewEntry(sess.Metadata.WorkspacePath); ok {
		return previewutil.StoredEntry{Scope: previewutil.StoredEntryScopeWorkspace, Path: entry}, true
	}
	return previewutil.StoredEntry{}, false
}

func previewOriginAssetPath(entry, requestPath string) string {
	requested := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if requested == "" || requested == "." {
		return entry
	}
	root := path.Dir(entry)
	if root == "." {
		return requested
	}
	if requested == root {
		return entry
	}
	requested = strings.TrimPrefix(requested, root+"/")
	return path.Join(root, requested)
}

func previewOriginArtifactAssetPath(entry, requestPath string) string {
	requested := strings.TrimPrefix(path.Clean("/"+requestPath), "/")
	if rel, ok := previewutil.ArtifactEntryRelative(requested); ok {
		return previewOriginAssetPath(entry, rel)
	}
	return previewOriginAssetPath(entry, requestPath)
}

// secretFromTokenless reports whether a request the daemon served without its
// install token resolves to a credential-bearing file
// (previewutil.IsSecretPath). assetPath is the final path under the served
// root, after a preview origin mapped the URL under its stored entry, so an
// entry such as ".aws/report.html" cannot expose ".aws/credentials" through a
// URL that names neither.
func secretFromTokenless(r *http.Request, assetPath string) bool {
	if !requestscope.IsTokenless(r.Context()) {
		return false
	}
	clean, ok := previewutil.CleanWorkspacePath(assetPath)
	return ok && previewutil.IsSecretPath(clean)
}
