package controllers_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/attachmentstore"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/httpd/controllers"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

// TestSessionsAPI_ArtifactRawURLIsUnambiguousUnderWorkspaceCollision covers
// the raw-content URL exported for non-HTML artifacts (markdown/generic files).
// Unlike the legacy __ao_artifacts__/ path-prefix route,
// rawUrl is built on the artifact preview origin — a distinct host from the
// workspace preview origin — so a real workspace file at the same literal
// path cannot shadow it.
func TestSessionsAPI_ArtifactRawURLIsUnambiguousUnderWorkspaceCollision(t *testing.T) {
	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "report.md"), []byte("artifact report content"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	// A real workspace file at the literal path the legacy marker scheme
	// would have used for this same artifact.
	workspace := t.TempDir()
	collidingPath := filepath.Join(workspace, "__ao_artifacts__", "report.md")
	if err := os.MkdirAll(filepath.Dir(collidingPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace collision dir: %v", err)
	}
	if err := os.WriteFile(collidingPath, []byte("workspace collision content"), 0o644); err != nil {
		t.Fatalf("write colliding workspace file: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.OutputType = domain.SessionOutputArtifact
	s.Metadata.ArtifactDir = artifactDir
	s.Metadata.WorkspacePath = workspace
	s.ArtifactFiles = []domain.SessionArtifactFile{
		{
			Path:      "report.md",
			Name:      "report.md",
			Kind:      domain.SessionArtifactMarkdown,
			Size:      24,
			UpdatedAt: time.Now().UTC().Truncate(time.Second),
		},
	}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1", "")
	if status != http.StatusOK {
		t.Fatalf("GET session = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			ArtifactFiles []struct {
				Path   string `json:"path"`
				RawURL string `json:"rawUrl"`
			} `json:"artifactFiles"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if len(resp.Session.ArtifactFiles) != 1 {
		t.Fatalf("artifactFiles = %+v, want 1", resp.Session.ArtifactFiles)
	}
	rawURL := resp.Session.ArtifactFiles[0].RawURL
	if !strings.Contains(rawURL, "ao-preview-artifact.") || !strings.HasSuffix(rawURL, "/report.md?raw=true") {
		t.Fatalf("rawUrl = %q, want an ao-preview-artifact origin with no path marker", rawURL)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse rawUrl: %v", err)
	}
	directBody, directStatus, _ := doPreviewOriginRequest(t, srv, rawURL, parsed.EscapedPath()+"?"+parsed.RawQuery)
	if directStatus != http.StatusOK {
		t.Fatalf("GET rawUrl = %d, want 200; body=%s", directStatus, directBody)
	}
	if !bytes.Contains(directBody, []byte("artifact report content")) {
		t.Fatalf("rawUrl body = %q, want artifact content, not the colliding workspace file's", directBody)
	}

	// The colliding workspace file, addressed on the ordinary (non-artifact)
	// preview origin at its own literal path, is unaffected.
	workspacePreviewURL, err := previewutil.FileURL(srv.URL, "tp-1", "__ao_artifacts__/report.md")
	if err != nil {
		t.Fatalf("build workspace preview URL: %v", err)
	}
	collisionBody, collisionStatus, _ := doPreviewOriginRequest(t, srv, workspacePreviewURL, "/__ao_artifacts__/report.md")
	if collisionStatus != http.StatusOK {
		t.Fatalf("GET colliding workspace file = %d, want 200; body=%s", collisionStatus, collisionBody)
	}
	if !bytes.Contains(collisionBody, []byte("workspace collision content")) {
		t.Fatalf("colliding workspace body = %q, want the workspace file's own content", collisionBody)
	}
}

// TestSessionsAPI_PreviewFileWorkspaceFileWinsOverArtifactNamespaceCollision
// covers the legacy /preview/files/* route directly: __ao_artifacts__/ is a
// reserved marker Tallpine prepends itself, but it was a valid workspace-relative
// path before artifact previews existed. A real workspace file at that exact
// literal path must still be served, not silently swapped for the artifact
// directory's unrelated content.
func TestSessionsAPI_PreviewFileWorkspaceFileWinsOverArtifactNamespaceCollision(t *testing.T) {
	workspace := t.TempDir()
	collidingPath := filepath.Join(workspace, "__ao_artifacts__", "index.html")
	if err := os.MkdirAll(filepath.Dir(collidingPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace collision dir: %v", err)
	}
	if err := os.WriteFile(collidingPath, []byte("workspace content"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	s.Metadata.ArtifactDir = artifactDir
	svc.sessions["tp-1"] = s

	// A second session with no colliding workspace file at that literal path,
	// to positively confirm the artifact directory still serves normally when
	// there is nothing to collide with.
	noCollisionArtifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(noCollisionArtifactDir, "index.html"), []byte("artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}
	s2 := s
	s2.ID = "tp-2"
	s2.Metadata.WorkspacePath = t.TempDir()
	s2.Metadata.ArtifactDir = noCollisionArtifactDir
	svc.sessions["tp-2"] = s2

	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/__ao_artifacts__/index.html", "")
	if status != http.StatusOK {
		t.Fatalf("GET preview file = %d, want 200; body=%s", status, body)
	}
	if !bytes.Contains(body, []byte("workspace content")) {
		t.Fatalf("served body = %q, want the colliding workspace file's content, not the artifact directory's", body)
	}

	noCollisionBody, noCollisionStatus, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-2/preview/files/__ao_artifacts__/index.html", "")
	if noCollisionStatus != http.StatusOK {
		t.Fatalf("GET non-colliding artifact preview = %d, want 200; body=%s", noCollisionStatus, noCollisionBody)
	}
	if !bytes.Contains(noCollisionBody, []byte("artifact content")) {
		t.Fatalf("served body = %q, want artifact directory content", noCollisionBody)
	}
}

// TestSessionsAPI_PreviewOriginWorkspaceFileWinsOverArtifactNamespaceCollision
// covers the same __ao_artifacts__/ namespace collision as the legacy-route
// test above, but on the isolated-origin PreviewOrigin route — the primary
// Browser route the desktop app actually uses. A stored preview URL pointing
// at that literal path must resolve to the real workspace file that existed
// there before artifact previews did, not to the unrelated artifact
// directory content.
func TestSessionsAPI_PreviewOriginWorkspaceFileWinsOverArtifactNamespaceCollision(t *testing.T) {
	workspace := t.TempDir()
	collidingPath := filepath.Join(workspace, "__ao_artifacts__", "index.html")
	if err := os.MkdirAll(filepath.Dir(collidingPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace collision dir: %v", err)
	}
	if err := os.WriteFile(collidingPath, []byte("workspace content"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	s.Metadata.ArtifactDir = artifactDir
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	previewURL, err := previewutil.FileURL(srv.URL, "tp-1", "__ao_artifacts__/index.html")
	if err != nil {
		t.Fatalf("build preview URL: %v", err)
	}
	s = svc.sessions["tp-1"]
	s.Metadata.PreviewURL = previewURL
	svc.sessions["tp-1"] = s

	body, status, _ := doPreviewOriginRequest(t, srv, previewURL, "/")
	if status != http.StatusOK {
		t.Fatalf("GET preview origin = %d, want 200; body=%s", status, body)
	}
	if !bytes.Contains(body, []byte("workspace content")) {
		t.Fatalf("served body = %q, want the colliding workspace file's content, not the artifact directory's", body)
	}
}

// TestSessionsAPI_PreviewOriginArtifactLinkRequestPathWorkspaceFileWins covers
// the fast path in previewOriginEntry that decodes __ao_artifacts__/ directly
// from the incoming request path (added for the Summary panel's artifact
// links, which are built via previewutil.FileURL and navigated to directly —
// independent of any stored session.Metadata.PreviewURL). The sibling test
// above only exercises requestPath "/" against a stored PreviewURL and does
// not reach this fast path at all, so it could not have caught this: a real
// workspace file at the literal requested __ao_artifacts__/ path must still
// win over the artifact directory here too.
func TestSessionsAPI_PreviewOriginArtifactLinkRequestPathWorkspaceFileWins(t *testing.T) {
	workspace := t.TempDir()
	collidingPath := filepath.Join(workspace, "__ao_artifacts__", "index.html")
	if err := os.MkdirAll(filepath.Dir(collidingPath), 0o755); err != nil {
		t.Fatalf("mkdir workspace collision dir: %v", err)
	}
	if err := os.WriteFile(collidingPath, []byte("workspace content"), 0o644); err != nil {
		t.Fatalf("write workspace file: %v", err)
	}

	artifactDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(artifactDir, "index.html"), []byte("artifact content"), 0o644); err != nil {
		t.Fatalf("write artifact file: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	s.Metadata.ArtifactDir = artifactDir
	// No PreviewURL stored: an artifact-panel link navigates directly to the
	// origin with the scope encoded in the request path itself.
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	linkURL, err := previewutil.FileURL(srv.URL, "tp-1", "__ao_artifacts__/index.html")
	if err != nil {
		t.Fatalf("build artifact link URL: %v", err)
	}

	body, status, _ := doPreviewOriginRequest(t, srv, linkURL, "/__ao_artifacts__/index.html")
	if status != http.StatusOK {
		t.Fatalf("GET artifact link = %d, want 200; body=%s", status, body)
	}
	if !bytes.Contains(body, []byte("workspace content")) {
		t.Fatalf("served body = %q, want the colliding workspace file's content, not the artifact directory's", body)
	}
}

func TestSessionsAPI_PreviewServesSelectedStaticIndex(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(`<link rel="stylesheet" href="styles.css"><script src="app.js"></script>`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "styles.css"), []byte(`body { color: red; }`), 0o644); err != nil {
		t.Fatalf("write css: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "index.html"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/preview", "")
	if status != http.StatusOK {
		t.Fatalf("preview = %d, want 200; body=%s", status, body)
	}
	var preview struct {
		SessionID  string `json:"sessionId"`
		PreviewURL string `json:"previewUrl"`
		Entry      string `json:"entry"`
	}
	mustJSON(t, body, &preview)
	if preview.SessionID != "tp-1" || preview.Entry != "index.html" || preview.PreviewURL == "" {
		t.Fatalf("preview response = %#v", preview)
	}
	if strings.Contains(preview.PreviewURL, workspace) {
		t.Fatalf("preview leaked workspace path: %s", preview.PreviewURL)
	}
	parsed, err := url.Parse(preview.PreviewURL)
	if err != nil {
		t.Fatalf("parse preview URL: %v", err)
	}
	if !strings.HasSuffix(parsed.Hostname(), ".localhost") || parsed.Path != "/index.html" {
		t.Fatalf("preview URL = %q, want isolated localhost origin ending in /index.html", preview.PreviewURL)
	}
	body, status, headers := doPreviewOriginRequest(t, srv, preview.PreviewURL, "/")
	if status != http.StatusOK {
		t.Fatalf("preview file = %d, want 200; body=%s", status, body)
	}
	if !strings.Contains(headers.Get("Content-Type"), "text/html") {
		t.Fatalf("content type = %q, want text/html", headers.Get("Content-Type"))
	}
	if !strings.Contains(string(body), "styles.css") {
		t.Fatalf("preview body did not serve index: %s", body)
	}
}

func TestSessionsAPI_PreviewHonorsSelectionInsteadOfRepositoryIndex(t *testing.T) {
	for _, selected := range []string{"", "dist/app.html", "missing.html", "http://localhost:5173/app", "https://example.com/demo"} {
		t.Run(selected, func(t *testing.T) {
			svc := newFakeSessionService()
			workspace := t.TempDir()
			if err := os.MkdirAll(filepath.Join(workspace, "dist"), 0o755); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"index.html", "dist/app.html"} {
				if err := os.WriteFile(filepath.Join(workspace, file), []byte(file), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s := svc.sessions["tp-1"]
			s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: selected}
			svc.sessions["tp-1"] = s
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/preview", "")
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%s", status, body)
			}
			var result controllers.SessionPreviewResponse
			mustJSON(t, body, &result)
			switch selected {
			case "dist/app.html":
				if result.Entry != selected || !strings.HasSuffix(result.PreviewURL, "/dist/app.html") {
					t.Fatalf("preview=%+v", result)
				}
			case "", "missing.html":
				if result.Entry != "" || result.PreviewURL != "" {
					t.Fatalf("unselected preview=%+v", result)
				}
			default:
				if result.Entry != "" || result.PreviewURL != selected {
					t.Fatalf("URL preview=%+v", result)
				}
			}
		})
	}
}

func TestSessionsAPI_PreviewArtifactOriginUsesArtifactFiles(t *testing.T) {
	svc := newFakeSessionService()
	workspace, artifacts := t.TempDir(), t.TempDir()
	for root, text := range map[string]string{workspace: "workspace", artifacts: "artifact"} {
		for _, file := range []string{"index.html", "style.css", "test page (1).html", "__ao_artifacts__/index.html"} {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, file), []byte(text), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	selected, err := previewutil.ArtifactFileURL("http://127.0.0.1:3002", "tp-1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, ArtifactDir: artifacts, PreviewURL: selected}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/preview", "")
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var result controllers.SessionPreviewResponse
	mustJSON(t, body, &result)
	if result.Source != "artifact" || result.Entry != "index.html" {
		t.Fatalf("preview=%+v", result)
	}
	for _, file := range []string{"index.html", "style.css", "test%20page%20%281%29.html"} {
		body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/preview/artifacts/"+file, "")
		if status != http.StatusOK || string(body) != "artifact" {
			t.Fatalf("%s: status=%d body=%s", file, status, body)
		}
	}
	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/tp-1/preview/artifacts/../outside.html", "")
	if status != http.StatusNotFound {
		t.Fatalf("traversal: status=%d body=%s", status, body)
	}
}

func TestSessionsAPI_PreviewRejectsSessionIDTooLongForHostname(t *testing.T) {
	svc := newFakeSessionService()
	id := domain.SessionID(strings.Repeat("x", 143))
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("preview"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	svc.sessions[id] = domain.Session{SessionRecord: domain.SessionRecord{ID: id, Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "index.html"}}}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/"+string(id)+"/preview", "")
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "PREVIEW_SESSION_ID_UNSUPPORTED")
}

func TestSessionsAPI_SetPreviewExplicitURLPersists(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{"url":"http://localhost:5173/"}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if resp.Session.PreviewURL != "http://localhost:5173/" {
		t.Fatalf("response previewUrl = %q, want explicit url", resp.Session.PreviewURL)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://localhost:5173/" {
		t.Fatalf("persisted previewUrl = %q, want explicit url", got)
	}
}

func TestSessionsAPI_SetPreviewEmptyURLAutodetectsIndex(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if !strings.Contains(resp.Session.PreviewURL, "/index.html") {
		t.Fatalf("response previewUrl = %q, want autodetected index.html URL", resp.Session.PreviewURL)
	}
	if strings.Contains(resp.Session.PreviewURL, workspace) {
		t.Fatalf("preview leaked workspace path: %s", resp.Session.PreviewURL)
	}
}

func TestSessionsAPI_SetPreviewEmptyURLDoesNotFallbackToMarkdown(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	// A Markdown-rich repo with no index.html: the old behavior fell back to
	// mostRecentPreviewable and picked README.md. The fix restricts bare
	// tallpine preview to real static entrypoints, so this should 404.
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("# project"), 0o644); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("set preview with only .md = %d, want 404; body=%s", status, body)
	}
}

func TestSessionsAPI_SetPreviewEmptyURLPrefersWorkspaceEntryOverExistingTarget(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	// An index.html exists, so bare `tallpine preview` returns to the workspace entry
	// instead of sticking to the last explicit target.
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "http://localhost:4321/docs"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if !strings.HasSuffix(resp.Session.PreviewURL, "/index.html") {
		t.Fatalf("response previewUrl = %q, want workspace index preview URL", resp.Session.PreviewURL)
	}
}

func TestSessionsAPI_SetPreviewEmptyURLNormalizesExistingRelativeTarget(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "index.html"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if !strings.HasSuffix(resp.Session.PreviewURL, "/index.html") {
		t.Fatalf("response previewUrl = %q, want index.html preview URL", resp.Session.PreviewURL)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != resp.Session.PreviewURL {
		t.Fatalf("persisted previewUrl = %q, want normalized response URL %q", got, resp.Session.PreviewURL)
	}
}

func TestSessionsAPI_SetPreviewEmptyURLReusesExistingTargetWhenNoEntryExists(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: t.TempDir(), PreviewURL: "http://localhost:4321/docs"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if resp.Session.PreviewURL != "http://localhost:4321/docs" {
		t.Fatalf("response previewUrl = %q, want reused existing target", resp.Session.PreviewURL)
	}
}

func TestSessionsAPI_SetPreviewLocalRelativePathResolvesToPreviewOrigin(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "dist"), 0o755); err != nil {
		t.Fatalf("mkdir dist: %v", err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dist", "index.html"), []byte(`<html></html>`), 0o644); err != nil {
		t.Fatalf("write dist index: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{"url":"./dist/index.html","requireWorkspaceFile":true}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if !strings.HasSuffix(resp.Session.PreviewURL, "/dist/index.html") {
		t.Fatalf("response previewUrl = %q, want dist/index.html preview URL", resp.Session.PreviewURL)
	}
	if strings.Contains(resp.Session.PreviewURL, workspace) {
		t.Fatalf("preview leaked workspace path: %s", resp.Session.PreviewURL)
	}
	// The resolved preview origin actually serves the local file.
	fileBody, fileStatus, _ := doPreviewOriginRequest(t, srv, resp.Session.PreviewURL, "/")
	if fileStatus != http.StatusOK {
		t.Fatalf("serve local file = %d, want 200; body=%s", fileStatus, fileBody)
	}
}

func TestSessionsAPI_PreviewFileRawMarkdownBypassesHTMLRendering(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	const markdown = "# Artifact notes\n\nhello\n"
	if err := os.WriteFile(filepath.Join(workspace, "notes.md"), []byte(markdown), 0o644); err != nil {
		t.Fatalf("write markdown: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/notes.md", "")
	if status != http.StatusOK {
		t.Fatalf("render markdown preview = %d, want 200; body=%s", status, body)
	}
	if !strings.Contains(headers.Get("Content-Type"), "text/html") || !bytes.Contains(body, []byte("<!doctype html>")) {
		t.Fatalf("rendered markdown response content-type=%q body=%q, want HTML document", headers.Get("Content-Type"), body)
	}

	for _, raw := range []string{"1", "true"} {
		body, status, headers = doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/notes.md?raw="+raw, "")
		if status != http.StatusOK {
			t.Fatalf("raw=%s markdown preview = %d, want 200; body=%s", raw, status, body)
		}
		if got := string(body); got != markdown {
			t.Fatalf("raw=%s markdown body = %q, want %q", raw, got, markdown)
		}
		if strings.Contains(headers.Get("Content-Type"), "text/html") {
			t.Fatalf("raw=%s markdown content type = %q, want non-HTML", raw, headers.Get("Content-Type"))
		}
	}
}

func TestSessionsAPI_SetPreviewServesBrowserDisplayableArtifacts(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		contents    []byte
		contentType string
	}{
		{name: "PDF", path: "artifacts/report.pdf", contents: []byte("%PDF-1.4\n%%EOF\n"), contentType: "application/pdf"},
		{name: "PNG", path: "artifacts/mockup.png", contents: []byte("\x89PNG\r\n\x1a\n"), contentType: "image/png"},
		{name: "SVG", path: "artifacts/diagram.svg", contents: []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), contentType: "image/svg+xml"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			workspace := t.TempDir()
			artifact := filepath.Join(workspace, filepath.FromSlash(tc.path))
			if err := os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
				t.Fatalf("mkdir artifact dir: %v", err)
			}
			if err := os.WriteFile(artifact, tc.contents, 0o644); err != nil {
				t.Fatalf("write artifact: %v", err)
			}
			session := svc.sessions["tp-1"]
			session.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
			svc.sessions["tp-1"] = session
			srv := newSessionTestServer(t, svc)

			request := `{"url":` + strconv.Quote(tc.path) + `}`
			body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview", request)
			if status != http.StatusOK {
				t.Fatalf("set artifact preview = %d body=%s", status, body)
			}
			var response struct {
				Session struct {
					PreviewURL string `json:"previewUrl"`
				} `json:"session"`
			}
			mustJSON(t, body, &response)
			if !strings.HasSuffix(response.Session.PreviewURL, "/"+tc.path) {
				t.Fatalf("preview URL = %q, want suffix /%s", response.Session.PreviewURL, tc.path)
			}

			served, servedStatus, headers := doPreviewOriginRequest(t, srv, response.Session.PreviewURL, "/")
			if servedStatus != http.StatusOK {
				t.Fatalf("serve artifact = %d body=%q", servedStatus, served)
			}
			if got := headers.Get("Content-Type"); !strings.HasPrefix(got, tc.contentType) {
				t.Fatalf("Content-Type = %q, want %q", got, tc.contentType)
			}
		})
	}
}

func TestSessionsAPI_PreviewOriginResolvesRootRelativeAssetsFromEntryDirectory(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	for _, dir := range []string{
		filepath.Join(workspace, "dist", "assets"),
		filepath.Join(workspace, "dist", "fonts"),
		filepath.Join(workspace, "assets"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	files := map[string]string{
		filepath.Join(workspace, "dist", "index.html"):         `<link rel="stylesheet" href="/assets/app.css"><script type="module" src="/assets/app.js"></script>`,
		filepath.Join(workspace, "dist", "assets", "app.css"):  `@font-face { src: url("/fonts/app.woff2") }`,
		filepath.Join(workspace, "dist", "assets", "app.js"):   `document.body.dataset.loaded = "yes"`,
		filepath.Join(workspace, "dist", "fonts", "app.woff2"): "preview-font",
		// A conflicting workspace-root asset proves /assets is mounted relative
		// to the selected deployment directory, not the workspace root.
		filepath.Join(workspace, "assets", "app.css"): "wrong-root",
	}
	for file, contents := range files {
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview", `{"url":"./dist/index.html"}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)

	for requestPath, want := range map[string]string{
		"/":                   `/assets/app.css`,
		"/assets/app.css":     `/fonts/app.woff2`,
		"/dist/assets/app.js": `dataset.loaded`,
		"/fonts/app.woff2":    `preview-font`,
	} {
		assetBody, assetStatus, _ := doPreviewOriginRequest(t, srv, resp.Session.PreviewURL, requestPath)
		if assetStatus != http.StatusOK {
			t.Errorf("GET %s = %d, want 200; body=%s", requestPath, assetStatus, assetBody)
			continue
		}
		if !strings.Contains(string(assetBody), want) {
			t.Errorf("GET %s body = %q, want content containing %q", requestPath, assetBody, want)
		}
	}

	// Retain the old API route as a compatibility surface for stored URLs and
	// external callers while new previews use the isolated origin.
	legacyBody, legacyStatus, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/dist/assets/app.css", "")
	if legacyStatus != http.StatusOK || !strings.Contains(string(legacyBody), "/fonts/app.woff2") {
		t.Fatalf("legacy preview route = %d, body=%q; want existing file response", legacyStatus, legacyBody)
	}
}

func TestSessionsAPI_PreviewFileServesCanonicalAttachmentWithoutWorkspace(t *testing.T) {
	dataDir := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "tp-1")
	if err := os.MkdirAll(workspace, 0o750); err != nil {
		t.Fatal(err)
	}
	want := []byte("durable-image-bytes")
	store := attachmentstore.New(dataDir)
	if err := store.Put(context.Background(), "tp-1", workspace, "attachment-durable.png", want); err != nil {
		t.Fatalf("Put attachment: %v", err)
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}

	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = session
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet,
		"/api/v1/sessions/tp-1/preview/files/.tallpine/attachments/attachment-durable.png?cache-bust=1", "")
	if status != http.StatusOK {
		t.Fatalf("canonical attachment response = %d, body=%s", status, body)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("canonical attachment body = %q, want %q", body, want)
	}
}

func TestSessionsAPI_PreviewFileDoesNotCrossSessionThroughCanonicalSymlink(t *testing.T) {
	dataDir := t.TempDir()
	workspace := t.TempDir()
	name := "attachment-private.png"
	store := attachmentstore.New(dataDir)
	if err := store.Put(context.Background(), "tp-1", workspace, name, []byte("session-one-bytes")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tp-1", filepath.Join(dataDir, "attachments", "tp-2")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	svc := newFakeSessionService()
	second := svc.sessions["tp-1"]
	second.ID = "tp-2"
	second.Metadata.WorkspacePath = t.TempDir()
	svc.sessions[second.ID] = second
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(
		config.Config{DataDir: dataDir}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{},
	))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodGet,
		"/api/v1/sessions/tp-2/preview/files/.tallpine/attachments/"+name, "")
	if status != http.StatusNotFound || !bytes.Contains(body, []byte(`"code":"PREVIEW_FILE_NOT_FOUND"`)) {
		t.Fatalf("cross-session preview = %d, %s; want PREVIEW_FILE_NOT_FOUND", status, body)
	}
}

func TestSessionsAPI_PreviewOriginErrorContract(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("preview"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)
	validURL := mustPreviewFileURL(t, srv, "tp-1", "index.html")

	tests := []struct {
		name       string
		method     string
		previewURL string
		path       string
		wantStatus int
		wantCode   string
		wantAllow  string
	}{
		{name: "method", method: http.MethodPost, previewURL: validURL, path: "/", wantStatus: http.StatusMethodNotAllowed, wantCode: "METHOD_NOT_ALLOWED", wantAllow: "GET, HEAD"},
		{name: "unknown session", method: http.MethodGet, previewURL: mustPreviewFileURL(t, srv, "tallpine-missing", "index.html"), path: "/", wantStatus: http.StatusNotFound, wantCode: "SESSION_NOT_FOUND"},
		{name: "missing asset", method: http.MethodGet, previewURL: validURL, path: "/missing.css", wantStatus: http.StatusNotFound, wantCode: "PREVIEW_FILE_NOT_FOUND"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, status, headers := doPreviewOriginMethod(t, srv, tc.method, tc.previewURL, tc.path)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.wantStatus, body)
			}
			if got := headers.Get("Allow"); got != tc.wantAllow {
				t.Fatalf("Allow = %q, want %q", got, tc.wantAllow)
			}
			var got struct {
				Code      string `json:"code"`
				RequestID string `json:"requestId"`
			}
			mustJSON(t, body, &got)
			if got.Code != tc.wantCode || got.RequestID == "" {
				t.Fatalf("error = %#v, want code %q and requestId", got, tc.wantCode)
			}
		})
	}

	empty := t.TempDir()
	s = svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: empty}
	svc.sessions["tp-1"] = s
	body, status, _ := doPreviewOriginRequest(t, srv, validURL, "/")
	assertErrorCode(t, body, status, http.StatusNotFound, "NO_PREVIEW_ENTRY")
}

func TestSessionsAPI_ArtifactMediaETagTracksRapidReplacements(t *testing.T) {
	for _, name := range []string{"image.png", "video.webm", "audio.mp3"} {
		t.Run(name, func(t *testing.T) {
			srv, root := artifactFileServer(t, map[string]string{name: "first clip"})
			file := filepath.Join(root, name)
			modified := time.Date(2026, time.October, 17, 12, 0, 0, 100_000_000, time.UTC)
			write := func(content string, stamp time.Time) {
				t.Helper()
				if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			get := func(headers map[string]string) (*http.Response, string) {
				t.Helper()
				req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/files/"+name+"?source=artifact&raw=true", nil)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Origin", "http://localhost:5173")
				for key, value := range headers {
					req.Header.Set(key, value)
				}
				resp, err := srv.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatal(err)
				}
				return resp, string(body)
			}

			write("first clip", modified)
			first, body := get(nil)
			etag, lastModified := first.Header.Get("ETag"), first.Header.Get("Last-Modified")
			if first.StatusCode != http.StatusOK || body != "first clip" || etag == "" || lastModified == "" {
				t.Fatalf("initial response: status=%d body=%q headers=%v", first.StatusCode, body, first.Header)
			}
			if first.Header.Get("Access-Control-Allow-Origin") != "http://localhost:5173" || first.Header.Get("Access-Control-Expose-Headers") != "ETag" {
				t.Fatalf("ETag must be readable by the renderer: headers=%v", first.Header)
			}
			if first.Header.Get("Cache-Control") != "private, no-cache" {
				t.Fatalf("metadata refreshes must revalidate: headers=%v", first.Header)
			}
			if unchanged, body := get(map[string]string{"If-None-Match": etag}); unchanged.StatusCode != http.StatusNotModified || body != "" || unchanged.Header.Get("ETag") != etag {
				t.Fatalf("unchanged response: status=%d body=%q headers=%v", unchanged.StatusCode, body, unchanged.Header)
			}
			for _, replacement := range []string{"other clip", "a longer replacement clip"} {
				// First change only subsecond mtime, then only size, within one HTTP second.
				write(replacement, modified.Add(time.Millisecond))
				fresh, body := get(map[string]string{"If-None-Match": etag, "If-Modified-Since": lastModified})
				freshETag := fresh.Header.Get("ETag")
				if fresh.StatusCode != http.StatusOK || body != replacement || freshETag == "" || freshETag == etag || fresh.Header.Get("Last-Modified") != lastModified {
					t.Fatalf("same-second replacement: status=%d body=%q oldTag=%q headers=%v", fresh.StatusCode, body, etag, fresh.Header)
				}
				etag = freshETag
			}
			ranged, body := get(map[string]string{"Range": "bytes=2-5", "If-Range": etag})
			if ranged.StatusCode != http.StatusPartialContent || body != "long" || ranged.Header.Get("ETag") != etag {
				t.Fatalf("media range: status=%d body=%q headers=%v", ranged.StatusCode, body, ranged.Header)
			}
		})
	}
}

func TestSessionsAPI_ArtifactRawAPIStaysInsideArtifactDirectory(t *testing.T) {
	workspace, artifacts := t.TempDir(), t.TempDir()
	for _, file := range []struct{ root, name, content string }{
		{workspace, "report.md", "workspace collision"},
		{workspace, "workspace-only.md", "workspace secret"},
		{artifacts, "report.md", "# Artifact source"},
		{filepath.Dir(artifacts), "secret.md", "outside secret"},
	} {
		if err := os.WriteFile(filepath.Join(file.root, file.name), []byte(file.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	symlinkErr := os.Symlink(filepath.Join(workspace, "workspace-only.md"), filepath.Join(artifacts, "escape.md"))
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, ArtifactDir: artifacts}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)
	for _, tc := range []struct {
		path       string
		wantStatus int
	}{
		{"report.md", http.StatusOK},
		{"workspace-only.md", http.StatusNotFound},
		{"escape.md", http.StatusNotFound},
		{"../secret.md", http.StatusNotFound},
		{"%2e%2e/secret.md", http.StatusNotFound},
		{"%2e%2e%2fsecret.md", http.StatusNotFound},
		{"%2e%2e%5csecret.md", http.StatusNotFound},
	} {
		t.Run(tc.path, func(t *testing.T) {
			if tc.path == "escape.md" && symlinkErr != nil {
				t.Skipf("symlinks unavailable: %v", symlinkErr)
			}
			body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/"+tc.path+"?source=artifact&raw=true", "")
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", status, tc.wantStatus, body)
			}
			if status == http.StatusNotFound {
				assertErrorCode(t, body, status, http.StatusNotFound, "PREVIEW_FILE_NOT_FOUND")
			}
			if status == http.StatusOK && string(body) != "# Artifact source" {
				t.Fatalf("expected raw artifact Markdown, got %q", body)
			}
			if bytes.Contains(body, []byte("workspace secret")) || bytes.Contains(body, []byte("workspace collision")) || bytes.Contains(body, []byte("outside secret")) {
				t.Fatalf("artifact API leaked workspace content: %s", body)
			}
		})
	}
}

// Match encodeURIComponent's wire encoding, which differs from Go's path
// escaping for these names and makes chi route using URL.RawPath.
func TestSessionsAPI_PreviewAPIEncodedFilenames(t *testing.T) {
	workspace, artifacts := t.TempDir(), t.TempDir()
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata = domain.SessionMetadata{WorkspacePath: workspace, ArtifactDir: artifacts}
	svc.sessions["tp-1"] = session
	srv := newSessionTestServer(t, svc)
	for _, tc := range []struct{ name, escaped string }{
		{"report (1).md", "report%20(1).md"},
		{"Q&A.md", "Q%26A.md"},
		{"a+b.md", "a%2Bb.md"},
		{"chart@2x.md", "chart%402x.md"},
		{"notes, v2.md", "notes%2C%20v2.md"},
		{"10:00 notes.md", "10%3A00%20notes.md"},
		{"it's done.md", "it's%20done.md"},
		{"literal%20name.md", "literal%2520name.md"},
		{"literal%20+a.md", "literal%2520%2Ba.md"},
		{"café.md", "caf%C3%A9.md"},
		{"hash#.md", "hash%23.md"},
		{"question?.md", "question%3F.md"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if runtime.GOOS == "windows" && strings.ContainsAny(tc.name, ":?") {
				t.Skip("filename is not supported on Windows")
			}
			for _, root := range []struct{ name, path, query string }{
				{"workspace", workspace, ""}, {"artifact", artifacts, "&source=artifact"},
			} {
				t.Run(root.name, func(t *testing.T) {
					want := "# " + root.name + " " + tc.name
					if err := os.WriteFile(filepath.Join(root.path, tc.name), []byte(want), 0o644); err != nil {
						t.Fatal(err)
					}
					body, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/"+tc.escaped+"?raw=true"+root.query, "")
					if status != http.StatusOK || string(body) != want {
						t.Fatalf("status=%d body=%q, want raw %q", status, body, want)
					}
				})
			}
		})
	}
}

func TestSessionsAPI_PreviewRoutesRejectSymlinkOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.css")
	if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte("preview"), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	if err := os.WriteFile(outside, []byte("must-not-leak"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(workspace, "escape.css")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, ArtifactDir: workspace}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)
	previewURL := mustPreviewFileURL(t, srv, "tp-1", "index.html")

	for _, tc := range []struct {
		name string
		do   func() ([]byte, int, http.Header)
	}{
		{name: "isolated origin", do: func() ([]byte, int, http.Header) {
			return doPreviewOriginRequest(t, srv, previewURL, "/escape.css")
		}},
		{name: "artifact API route", do: func() ([]byte, int, http.Header) {
			return doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/escape.css?source=artifact", "")
		}},
		{name: "legacy route", do: func() ([]byte, int, http.Header) {
			return doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/escape.css", "")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, status, _ := tc.do()
			assertErrorCode(t, body, status, http.StatusNotFound, "PREVIEW_FILE_NOT_FOUND")
			if strings.Contains(string(body), "must-not-leak") {
				t.Fatalf("response leaked file outside workspace: %s", body)
			}
		})
	}
}

func TestSessionsAPI_PreviewOriginsIsolateConcurrentSessionsAndSurviveRouterRestart(t *testing.T) {
	svc := newFakeSessionService()
	previewURLs := make(map[domain.SessionID]string)
	for _, tc := range []struct {
		id      domain.SessionID
		content string
	}{{id: "tp-1", content: "session-one"}, {id: "tp-2", content: "session-two"}} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(`<link rel="stylesheet" href="/theme.css">`), 0o644); err != nil {
			t.Fatalf("write index: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workspace, "theme.css"), []byte(tc.content), 0o644); err != nil {
			t.Fatalf("write theme: %v", err)
		}
		s := domain.Session{SessionRecord: domain.SessionRecord{ID: tc.id, Kind: domain.KindWorker}}
		s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
		svc.sessions[tc.id] = s
	}

	srv := newSessionTestServer(t, svc)
	for id := range svc.sessions {
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/"+string(id)+"/preview", `{}`)
		if status != http.StatusOK {
			t.Fatalf("set preview %s = %d, want 200; body=%s", id, status, body)
		}
		var resp struct {
			Session struct {
				PreviewURL string `json:"previewUrl"`
			} `json:"session"`
		}
		mustJSON(t, body, &resp)
		previewURLs[id] = resp.Session.PreviewURL
	}

	for id, want := range map[domain.SessionID]string{"tp-1": "session-one", "tp-2": "session-two"} {
		body, status, _ := doPreviewOriginRequest(t, srv, previewURLs[id], "/theme.css")
		if status != http.StatusOK || string(body) != want {
			t.Fatalf("session %s asset = %d, %q; want 200, %q", id, status, body, want)
		}
	}

	// A new router has no in-memory preview registry. The persisted URL and
	// session workspace are sufficient to reconstruct the same virtual root.
	restarted := newSessionTestServer(t, svc)
	body, status, _ := doPreviewOriginRequest(t, restarted, previewURLs["tp-1"], "/theme.css")
	if status != http.StatusOK || string(body) != "session-one" {
		t.Fatalf("asset after router restart = %d, %q; want 200, session-one", status, body)
	}
}

func TestSessionsAPI_SetPreviewAbsoluteWorkspaceFileUsesConfinedOrigin(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	file := filepath.Join(workspace, "implementation_plan.html")
	if err := os.WriteFile(file, []byte(`<html>workspace preview</html>`), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{"url":`+strconv.Quote(file)+`}`)
	if status != http.StatusOK {
		t.Fatalf("set preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	parsed, err := url.Parse(resp.Session.PreviewURL)
	if err != nil {
		t.Fatalf("parse preview url: %v", err)
	}
	if parsed.Scheme != "http" || !strings.HasPrefix(parsed.Hostname(), "ao-preview.") {
		t.Fatalf("previewUrl = %q, want confined preview origin", resp.Session.PreviewURL)
	}
	previewBody, previewStatus, _ := doPreviewOriginRequest(t, srv, resp.Session.PreviewURL, "/")
	if previewStatus != http.StatusOK || string(previewBody) != `<html>workspace preview</html>` {
		t.Fatalf("workspace preview = %d, %q; want 200 and workspace file", previewStatus, previewBody)
	}
}

func TestSessionsAPI_SetPreviewRejectsAbsoluteFilesOutsideWorkspace(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	s.Metadata.PreviewURL = "http://localhost:4321/docs"
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	fileURLPath := filepath.ToSlash(outside)
	if filepath.VolumeName(outside) != "" {
		fileURLPath = "/" + fileURLPath
	}
	fileURL := (&url.URL{Scheme: "file", Path: fileURLPath}).String()
	for _, target := range []string{outside, fileURL} {
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview", `{"url":`+strconv.Quote(target)+`}`)
		if status != http.StatusForbidden || !bytes.Contains(body, []byte(`"code":"PREVIEW_FILE_OUTSIDE_WORKSPACE"`)) {
			t.Fatalf("set outside preview %q = %d, body=%s; want 403 workspace error", target, status, body)
		}
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://localhost:4321/docs" {
		t.Fatalf("persisted previewUrl = %q, want existing target preserved", got)
	}
}

func TestSessionsAPI_SetPreviewRejectsRelativeParentTraversal(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = t.TempDir()
	s.Metadata.PreviewURL = "http://localhost:4321/docs"
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	for _, target := range []string{"../README.md", "docs/../../README.md", `..\README.md`} {
		body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview", `{"url":`+strconv.Quote(target)+`}`)
		if status != http.StatusForbidden || !bytes.Contains(body, []byte(`"code":"PREVIEW_FILE_OUTSIDE_WORKSPACE"`)) {
			t.Fatalf("set parent traversal preview %q = %d, body=%s; want 403 workspace error", target, status, body)
		}
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://localhost:4321/docs" {
		t.Fatalf("persisted previewUrl = %q, want existing target preserved", got)
	}
}

func TestSessionsAPI_SetPreviewRejectsWorkspaceSymlinkEscape(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.html")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	escape := filepath.Join(workspace, "escape.html")
	if err := os.Symlink(outside, escape); err != nil {
		if runtime.GOOS == "windows" || errors.Is(err, os.ErrPermission) {
			t.Skipf("symlinks unavailable: %v", err)
		}
		t.Fatalf("create symlink escape: %v", err)
	}
	s := svc.sessions["tp-1"]
	s.Metadata.WorkspacePath = workspace
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview", `{"url":`+strconv.Quote(escape)+`}`)
	if status != http.StatusForbidden || !bytes.Contains(body, []byte(`"code":"PREVIEW_FILE_OUTSIDE_WORKSPACE"`)) {
		t.Fatalf("set symlink escape = %d, body=%s; want 403 workspace error", status, body)
	}
}

func TestSessionsAPI_SetPreviewMissingOrMalformedFileFailsWithoutOverwriting(t *testing.T) {
	svc := newFakeSessionService()
	workspace := t.TempDir()
	missing := filepath.Join(workspace, "implmentation_plan.html")
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "http://localhost:4321/docs"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	for _, testCase := range []struct {
		target               string
		requireWorkspaceFile bool
	}{
		{target: missing},
		{target: "file:///%"},
		{target: "reports/missing.html", requireWorkspaceFile: true},
	} {
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{"url":`+strconv.Quote(testCase.target)+`,"requireWorkspaceFile":`+strconv.FormatBool(testCase.requireWorkspaceFile)+`}`)
		if status != http.StatusNotFound || !bytes.Contains(body, []byte(`"code":"PREVIEW_FILE_NOT_FOUND"`)) {
			t.Fatalf("set unavailable file preview %q = %d, want 404; body=%s", testCase.target, status, body)
		}
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://localhost:4321/docs" {
		t.Fatalf("persisted previewUrl = %q, want existing target preserved", got)
	}
}

func TestSessionsAPI_SetPreviewBumpsRevisionOnSameURL(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	readRevision := func() int64 {
		body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{"url":"http://localhost:5173/"}`)
		if status != http.StatusOK {
			t.Fatalf("set preview = %d, want 200; body=%s", status, body)
		}
		var resp struct {
			Session struct {
				PreviewRevision int64 `json:"previewRevision"`
			} `json:"session"`
		}
		mustJSON(t, body, &resp)
		return resp.Session.PreviewRevision
	}
	first := readRevision()
	second := readRevision()
	if second <= first {
		t.Fatalf("revision did not advance on same-URL re-run: first=%d second=%d", first, second)
	}
}

func TestSessionsAPI_ClearPreviewResetsURL(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{PreviewURL: "http://localhost:5173/"}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/tp-1/preview", "")
	if status != http.StatusOK {
		t.Fatalf("clear preview = %d, want 200; body=%s", status, body)
	}
	var resp struct {
		Session struct {
			PreviewURL string `json:"previewUrl"`
		} `json:"session"`
	}
	mustJSON(t, body, &resp)
	if resp.Session.PreviewURL != "" {
		t.Fatalf("response previewUrl = %q, want empty after clear", resp.Session.PreviewURL)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "" {
		t.Fatalf("persisted previewUrl = %q, want empty after clear", got)
	}
}

func TestSessionsAPI_ClearPreviewNotFound(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/missing-1/preview", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_SetPreviewEmptyURLNoEntry(t *testing.T) {
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: t.TempDir()}
	svc.sessions["tp-1"] = s
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/tp-1/preview", `{}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "NO_PREVIEW_ENTRY")
}

func TestSessionsAPI_SetPreviewNotFound(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/missing-1/preview", `{"url":"http://x"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_LegacyPreviewDocumentsHaveNoAPIOrigin(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"index.html", "report.md", "image.svg", "asset.js"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("<script>window.preview=true</script>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	sess := svc.sessions["tp-1"]
	sess.Metadata.WorkspacePath = root
	svc.sessions["tp-1"] = sess
	srv := newSessionTestServer(t, svc)
	previewURL, err := previewutil.FileURL(srv.URL, "tp-1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "report.md", "image.svg", "asset.js"} {
		_, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/files/"+name, "")
		if status != 200 || headers.Get("Content-Security-Policy") != "sandbox allow-scripts allow-forms" || headers.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("legacy %s: status=%d headers=%v", name, status, headers)
		}
		_, status, headers = doPreviewOriginRequest(t, srv, previewURL, "/"+name)
		if status != 200 || headers.Get("Content-Security-Policy") != "" {
			t.Fatalf("isolated %s: status=%d headers=%v", name, status, headers)
		}
	}
}

// A preview origin mounts the stored entry's directory at its root, so a URL
// that names no secret can still resolve into a credential directory. Without
// the install token, the served path itself must not be a secret.
func TestPreviewOriginHidesSecretEntryDirectoryFromTokenlessReads(t *testing.T) {
	const token = "preview-install-token-0123456789abcdef"
	workspace := t.TempDir()
	for file, text := range map[string]string{
		".aws/report.html":             "private report",
		".aws/credentials":             "secret",
		".storybook-static/index.html": "storybook",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: ".aws/report.html"}
	svc.sessions["tp-1"] = s
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{DaemonToken: token}))
	t.Cleanup(srv.Close)
	previewURL, err := previewutil.FileURL(srv.URL, "tp-1", ".aws/report.html")
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := url.Parse(previewURL)

	get := func(path string, withToken bool) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, srv.URL+path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		req.Host = preview.Host
		if withToken {
			req.Header.Set(daemontoken.Header, token)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	for _, path := range []string{"/", "/report.html", "/credentials"} {
		if status, body := get(path, false); status != http.StatusNotFound || !strings.Contains(body, "PREVIEW_FILE_NOT_FOUND") {
			t.Errorf("tokenless GET %s = %d %q, want the missing-file 404", path, status, body)
		}
	}
	if status, body := get("/credentials", true); status != http.StatusOK || body != "secret" {
		t.Fatalf("GET /credentials with the token = %d %q", status, body)
	}

	// An ordinary dot-directory entry still serves without the token.
	s.Metadata.PreviewURL = ".storybook-static/index.html"
	svc.sessions["tp-1"] = s
	if status, body := get("/", false); status != http.StatusOK || body != "storybook" {
		t.Fatalf("tokenless GET / of a .storybook-static entry = %d %q", status, body)
	}
}

// The desktop Browser tab opens workspace-file links as
// /preview/files/<path> without the install token (useSessionBrowserLink ->
// openInBrowser(attachmentURL(...))). Ordinary dotfiles must still serve there;
// only credential-bearing files are refused.
func TestBrowserTabWorkspaceFileLinksServeOrdinaryDotfilesTokenless(t *testing.T) {
	workspace := t.TempDir()
	for file, text := range map[string]string{
		".github/workflows/ci.yml":    "on: push",
		".claude/settings.json":       "{}",
		".env.example":                "API_KEY=",
		".tallpine/attachments/a.txt": "attached",
		".env":                        "API_KEY=secret",
		".tallpine/launch.json":       "{}",
		".git/config":                 "[remote]",
		"index.html":                  "<p>hi</p>",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
	svc.sessions["tp-1"] = s
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, log, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{DaemonToken: "browser-tab-install-token-0123456789"}))
	t.Cleanup(srv.Close)
	for file, want := range map[string]string{
		".github/workflows/ci.yml":    "on: push",
		".claude/settings.json":       "{}",
		".env.example":                "API_KEY=",
		".tallpine/attachments/a.txt": "attached",
		".env":                        "",
		".tallpine/launch.json":       "",
		".git/config":                 "",
	} {
		// The Browser tab loads the isolated origin the daemon handed out,
		// which carries the session capability and needs no token.
		origin, err := previewutil.FileURL(srv.URL, "tp-1", "index.html")
		if err != nil {
			t.Fatal(err)
		}
		body, status, _ := doPreviewOriginRequest(t, srv, origin, "/"+file)
		if want == "" {
			if status != http.StatusNotFound || !strings.Contains(string(body), "PREVIEW_FILE_NOT_FOUND") {
				t.Errorf("tokenless %s = %d %s, want the missing-file 404", file, status, body)
			}
			continue
		}
		if status != http.StatusOK || string(body) != want {
			t.Errorf("tokenless %s = %d %q, want 200 %q", file, status, body, want)
		}
	}
}
