package controllers

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apispec"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	"tallpine.eu/tallpine/backend/internal/previewserver"
)

func (c *SessionsController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	// Selection is session-owned. Discovering a repository index here can
	// replace a running app (or an empty selection) with unrelated source HTML.
	res := SessionPreviewResponse{SessionID: sessionID(r)}
	if selected, ok := previewutil.StoredEntryFromPreview(sess.Metadata.PreviewURL, sess.ID); ok {
		root := sess.Metadata.WorkspacePath
		if selected.Scope == previewutil.StoredEntryScopeArtifact {
			root = sess.Metadata.ArtifactDir
		}
		if _, exists := previewutil.EntryAtPath(root, selected.Path); exists {
			res.Entry = selected.Path
			res.Source = string(selected.Scope)
			if selected.Scope == previewutil.StoredEntryScopeArtifact {
				res.PreviewURL, err = previewutil.ArtifactFileURL("http://"+r.Host, sessionID(r), selected.Path)
			} else {
				res.PreviewURL, err = previewFileURL(r, sessionID(r), selected.Path)
			}
			if err != nil {
				writePreviewResolveError(w, r, err)
				return
			}
		}
	} else if target, parseErr := url.Parse(strings.TrimSpace(sess.Metadata.PreviewURL)); parseErr == nil &&
		(target.Scheme == "http" || target.Scheme == "https") && target.Host != "" {
		res.PreviewURL = target.String()
	}

	envelope.WriteJSON(w, http.StatusOK, res)
}

// previewArtifactFile keeps artifact scope in the route so relative CSS,
// scripts, and images cannot fall back to same-named workspace files.
func (c *SessionsController) previewArtifactFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview/artifacts/*")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	asset := chi.URLParam(r, "*")
	if r.URL.RawPath != "" {
		if asset, err = url.PathUnescape(asset); err != nil {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
			return
		}
	}
	c.serveRootedPreviewFile(w, r, sess.Metadata.ArtifactDir, asset)
}

func (c *SessionsController) previewFile(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/preview/files/*")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	assetPath, _, err := wildcardPath(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_NOT_FOUND", "Preview not found", nil)
		return
	}
	if r.URL.Query().Get("source") == "artifact" {
		c.serveRootedPreviewFile(w, r, sess.Metadata.ArtifactDir, assetPath)
		return
	}
	c.serveWorkspacePreviewPath(w, r, sess, assetPath)
}

// serveWorkspacePreviewPath serves one workspace-relative path for a session:
// a staged attachment, a file in the artifact directory addressed through the
// reserved marker, or a workspace file. Shared by the legacy /preview/files
// route and the workspace files origin.
func (c *SessionsController) serveWorkspacePreviewPath(w http.ResponseWriter, r *http.Request, sess domain.Session, assetPath string) {
	if name, ok := attachmentstore.NameFromWorkspacePath(assetPath); ok && c.Attachments != nil {
		file, info, openErr := c.Attachments.Open(r.Context(), sess.ID, name)
		if openErr == nil {
			defer func() { _ = file.Close() }()
			serveOpenedPreviewFile(w, r, file, info, assetPath)
			return
		}
	}
	// __ao_artifacts__/ is a reserved marker Tallpine itself prepends when it builds
	// an artifact preview link (see ArtifactEntryPath), but it was a valid
	// workspace-relative path before artifact previews existed. A real
	// workspace file at that exact path must keep winning, so only route to
	// the artifact directory when no such workspace file actually exists.
	if rel, ok := previewutil.ArtifactEntryRelative(assetPath); ok {
		if _, existsInWorkspace := previewutil.EntryAtPath(sess.Metadata.WorkspacePath, assetPath); !existsInWorkspace {
			c.serveRootedPreviewFile(w, r, sess.Metadata.ArtifactDir, rel)
			return
		}
	}
	c.serveRootedPreviewFile(w, r, sess.Metadata.WorkspacePath, assetPath)
}

// previewGzipWriters reuses compressors: each holds about 800 KB of state, and
// a preview's first load compresses hundreds of modules at once.
var previewGzipWriters = sync.Pool{New: func() any {
	gz, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed)
	return gz
}}

// previewGzipMaxBytes bounds the bodies compressPreviewResponse compresses: it
// holds each whole while compressing it, and its gzip until the client has it.
// At 4 MiB that gzip is about 1 MB, one compressor's worth, and the bound still
// takes in a Vite dev server's pre-bundled dependencies (react-dom and
// lucide-react are about 1 MB). A larger body passes as sent.
const previewGzipMaxBytes = 4 << 20

// compressPreviewResponse gzips a dev server's uncompressed code and text
// before it crosses the network to the desktop. Dev servers rarely compress,
// and their unminified modules shrink several times. Only a body whose length
// the dev server announced, 1 KiB to 4 MiB, is compressed, and it is
// compressed whole before any of it is sent: a compressor is busy only while
// it runs, never while a slow link drains its output. A streamed body (SSR,
// RSC payloads, event and token streams) announces no length and must reach
// the client as written. A range is left as sent: its offsets count the
// uncompressed bytes.
func compressPreviewResponse(res *http.Response) {
	contentType := strings.ToLower(res.Header.Get("Content-Type"))
	compressible := strings.HasPrefix(contentType, "text/") || strings.Contains(contentType, "javascript") ||
		strings.Contains(contentType, "json") || strings.Contains(contentType, "svg") || strings.Contains(contentType, "wasm")
	// A 204 or 304 has ContentLength 0; a HEAD carries the GET's length.
	if !compressible || strings.Contains(contentType, "event-stream") || res.ContentLength < 1024 || res.ContentLength > previewGzipMaxBytes ||
		res.StatusCode == http.StatusPartialContent || res.Header.Get("Content-Encoding") != "" || res.Request == nil ||
		res.Request.Method == http.MethodHead || res.Request.Header.Get("Range") != "" || !acceptsGzip(res.Request.Header.Get("Accept-Encoding")) {
		return
	}
	body := make([]byte, res.ContentLength)
	_, err := io.ReadFull(res.Body, body)
	_ = res.Body.Close()
	if err != nil {
		// The dev server cut the body short, and nothing is sent yet: abort,
		// so the browser cannot take a shorter module for the whole one.
		panic(http.ErrAbortHandler)
	}
	var compressed bytes.Buffer
	gz, _ := previewGzipWriters.Get().(*gzip.Writer) // New makes nothing else
	gz.Reset(&compressed)
	_, _ = gz.Write(body) // into memory: cannot fail
	_ = gz.Close()
	gz.Reset(nil) // the pool keeps the compressor, not the response it wrote
	previewGzipWriters.Put(gz)
	// With a length, ReverseProxy copies in 32 KiB writes and never flushes.
	res.Body = io.NopCloser(&compressed)
	res.ContentLength = int64(compressed.Len())
	res.Header.Set("Content-Length", strconv.Itoa(compressed.Len()))
	res.Header.Set("Content-Encoding", "gzip")
	res.Header.Add("Vary", "Accept-Encoding")
	// The bytes differ now, so a strong validator would be a lie.
	if etag := res.Header.Get("ETag"); strings.HasPrefix(etag, `"`) {
		res.Header.Set("ETag", "W/"+etag)
	}
}

func acceptsGzip(acceptEncoding string) bool {
	for _, part := range strings.Split(acceptEncoding, ",") {
		coding, params, _ := strings.Cut(part, ";")
		if !strings.EqualFold(strings.TrimSpace(coding), "gzip") {
			continue
		}
		// A weight of 0 refuses gzip, however it is written: q=0, Q=0.000.
		for _, param := range strings.Split(params, ";") {
			if name, weight, _ := strings.Cut(param, "="); strings.EqualFold(strings.TrimSpace(name), "q") {
				q, err := strconv.ParseFloat(strings.TrimSpace(weight), 64)
				return err == nil && q > 0
			}
		}
		return true
	}
	return false
}

// previewTransport carries the desktop's previews and agent browser to this
// host's loopback servers. A desktop's HTTP/2 fan-in sends a preview's
// requests up to 100 at once, and DefaultTransport keeps only 2 idle
// connections per host: it would dial the rest again every burst.
var previewTransport = func() *http.Transport {
	transport := &http.Transport{}
	if defaultTransport, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = defaultTransport.Clone()
	}
	transport.MaxIdleConnsPerHost = 100
	return transport
}()

// previewApp reaches only the running, session-owned managed preview process.
// The selected URL comes from the manager, never from a caller-supplied port.
func (c *SessionsController) previewApp(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil || c.PreviewServer == nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_NOT_FOUND", "Preview not found", nil)
		return
	}
	// Every module a dev server serves passes through here: read the stored
	// row only, not the full session read model.
	sess, err := c.Svc.GetRecord(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	status := c.PreviewServer.Status(sess.ID)
	target, err := url.Parse(status.URL)
	if err != nil || status.State != previewserver.StateReady || status.TargetKind != previewserver.TargetApp ||
		status.URL != sess.Metadata.PreviewURL || target == nil || target.Scheme != "http" ||
		!isPreviewLoopback(target.Hostname()) || target.User != nil || target.Port() == "" ||
		strconv.Itoa(status.Port) != target.Port() || sess.IsTerminated {
		w.Header().Set("X-AO-Preview-Managed-Required", "1")
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PREVIEW_MANAGED_REQUIRED", "Run tallpine preview start in this session to open a remote live preview", nil)
		return
	}
	tail, escaped, err := wildcardPath(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_NOT_FOUND", "Preview not found", nil)
		return
	}
	proxy := &httputil.ReverseProxy{
		Transport: previewTransport,
		Rewrite: func(req *httputil.ProxyRequest) {
			appAuthorization := req.Out.Header.Get("X-AO-Preview-App-Authorization")
			hadPreviewOrigin := req.Out.Header.Get("X-AO-Preview-App-Origin") != ""
			req.SetURL(target)
			req.Out.Host = target.Host
			// Forward the client's escapes once: "%40" stays "%40", not "%2540".
			req.Out.URL.Path = "/" + strings.TrimPrefix(tail, "/")
			req.Out.URL.RawPath = ""
			if escaped != "" {
				req.Out.URL.RawPath = "/" + strings.TrimPrefix(escaped, "/")
			}
			req.Out.URL.RawQuery = req.In.URL.RawQuery
			// Tallpine credentials authenticate the tunnel, not the agent-run web app.
			req.Out.Header.Del("Authorization")
			req.Out.Header.Del("Proxy-Authorization")
			req.Out.Header.Del("X-AO-Preview-App-Authorization")
			req.Out.Header.Del("X-AO-Preview-App-Origin")
			req.Out.Header.Del("Origin")
			if appAuthorization != "" {
				req.Out.Header.Set("Authorization", appAuthorization)
			}
			if hadPreviewOrigin {
				req.Out.Header.Set("Origin", target.Scheme+"://"+target.Host)
			}
			req.Out.Header.Del(browserCapabilityHeader)
			cookies := req.Out.Cookies()
			req.Out.Header.Del("Cookie")
			for _, cookie := range cookies {
				if cookie.Name != "ao_conn" {
					req.Out.AddCookie(cookie)
				}
			}
		},
		ModifyResponse: func(res *http.Response) error {
			// Remote proxies publish this app on a separate origin, where the
			// sandbox is removed in favor of their existing preview policy.
			res.Header.Add("Content-Security-Policy", "sandbox allow-scripts allow-forms")
			res.Header.Set("X-AO-Preview-API-Sandbox", "1")
			if location := res.Header.Get("Location"); location != "" {
				if redirect, parseErr := url.Parse(location); parseErr == nil && redirect.IsAbs() && isPreviewLoopback(redirect.Hostname()) {
					if redirect.Port() != target.Port() {
						return errors.New("preview redirect to another local port")
					}
					res.Header.Set("Location", redirect.RequestURI())
				}
			}
			compressPreviewResponse(res)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(w, "Preview target unavailable or redirected to another local port", http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// serveWorkspacePreviewFile is the single serving path for both the legacy API
// route and isolated preview origins. OpenRoot keeps symlink traversal and the
// subsequent read on the same workspace-confined file handle.
func (c *SessionsController) serveRootedPreviewFile(w http.ResponseWriter, r *http.Request, rootPath, assetPath string) {
	if secretFromTokenless(r, assetPath) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	file, info, clean, err := previewutil.OpenWorkspaceFile(rootPath, assetPath)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	defer func() { _ = file.Close() }()
	serveOpenedPreviewFile(w, r, file, info, clean)
}

func serveOpenedPreviewFile(w http.ResponseWriter, r *http.Request, file *os.File, info fs.FileInfo, clean string) {
	sandboxLegacyPreview(w, r)

	if !previewutil.IsMarkdownPath(clean) || previewRawRequested(r) {
		// Last-Modified alone cannot distinguish replacements within one second.
		w.Header().Set("ETag", fmt.Sprintf(`"%d-%d"`, info.Size(), info.ModTime().UnixNano()))
		w.Header().Set("Access-Control-Expose-Headers", "ETag")
		w.Header().Set("Cache-Control", "private, no-cache")
		http.ServeContent(w, r, info.Name(), info.ModTime(), file)
		return
	}

	source, err := io.ReadAll(file)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PREVIEW_FILE_NOT_FOUND", "Preview file not found", nil)
		return
	}
	rendered, err := previewutil.RenderMarkdown(source, filepath.Base(clean))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = w.Write(rendered) //nolint:gosec // G705: legacy documents are sandboxed; isolated previews have no daemon API authority
	}
}

// Legacy API URLs must not give workspace HTML, Markdown or SVG the API's
// origin. The sandbox permits scripts/forms but deliberately omits same-origin.
// Normal isolated preview origins retain their own module/asset behavior.
func sandboxLegacyPreview(w http.ResponseWriter, r *http.Request) {
	if _, ok := previewutil.SessionIDFromHost(r.Host); ok {
		return
	}
	if _, ok := previewutil.SessionIDFromArtifactHost(r.Host); ok {
		return
	}
	// The files origin is deliberately not listed: it spans the whole workspace
	// and is reached by one click on any chat link, so its pages keep the
	// opaque-origin sandbox the legacy route gave them.
	if _, ok := previewutil.SessionIDFromInlineArtifactHost(r.Host); ok {
		return
	}
	w.Header().Add("Content-Security-Policy", "sandbox allow-scripts allow-forms")
	w.Header().Set("X-AO-Preview-API-Sandbox", "1")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

func previewRawRequested(r *http.Request) bool {
	raw := r.URL.Query().Get("raw")
	if raw == "" {
		return false
	}
	enabled, err := strconv.ParseBool(raw)
	return err == nil && enabled
}
