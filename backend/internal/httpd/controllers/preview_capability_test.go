package controllers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
)

const capabilityTestToken = "preview-capability-token-0123456789"

// capabilityServer serves two sessions, each with its own workspace, and
// returns the workspaces by id.
func capabilityServer(t *testing.T) *httptest.Server {
	t.Helper()
	svc := newFakeSessionService()
	for id, text := range map[domain.SessionID]string{"tp-1": "one", "tp-2": "two"} {
		workspace := t.TempDir()
		if err := os.WriteFile(filepath.Join(workspace, "index.html"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		s := svc.sessions["tp-1"]
		s.ID = id
		s.Metadata = domain.SessionMetadata{WorkspacePath: workspace}
		svc.sessions[id] = s
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, nil, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{DaemonToken: capabilityTestToken}))
	t.Cleanup(srv.Close)
	return srv
}

func previewHostFor(t *testing.T, srv *httptest.Server, id domain.SessionID) string {
	t.Helper()
	raw, err := previewutil.FileURL(srv.URL, id, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(raw)
	return u.Host
}

func getOnHost(t *testing.T, srv *httptest.Server, host string) (string, int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/index.html", http.NoBody)
	req.Host = host
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var b strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := resp.Body.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return b.String(), resp.StatusCode
}

func TestPreviewCapability_OriginRequiresTheCapabilityLabel(t *testing.T) {
	srv := capabilityServer(t)
	host := previewHostFor(t, srv, "tp-1")
	if body, status := getOnHost(t, srv, host); status != http.StatusOK || body != "one" {
		t.Fatalf("capability host = %d %q, want 200 \"one\"", status, body)
	}
	// The pre-capability URL: base32 id only, no capability label.
	labels := strings.Split(host, ".")
	idOnly := strings.Join(append(labels[:len(labels)-2:len(labels)-2], labels[len(labels)-1]), ".")
	if body, status := getOnHost(t, srv, idOnly); status == http.StatusOK || strings.Contains(body, "one") {
		t.Fatalf("id-only host = %d %q, want it refused", status, body)
	}
}

func TestPreviewCapability_OtherSessionsCapabilityDoesNotOpenThisSession(t *testing.T) {
	srv := capabilityServer(t)
	one := strings.Split(previewHostFor(t, srv, "tp-1"), ".")
	two := strings.Split(previewHostFor(t, srv, "tp-2"), ".")
	// tp-1's id labels carrying tp-2's capability label.
	forged := append(append([]string{}, one[:len(one)-2]...), two[len(two)-2], one[len(one)-1])
	if body, status := getOnHost(t, srv, strings.Join(forged, ".")); status == http.StatusOK || strings.Contains(body, "one") {
		t.Fatalf("forged host = %d %q, want it refused", status, body)
	}
	if body, status := getOnHost(t, srv, strings.Join(two, ".")); status != http.StatusOK || body != "two" {
		t.Fatalf("tp-2 own host = %d %q, want 200 \"two\"", status, body)
	}
}

func TestPreviewCapability_WorkspaceCapabilityDoesNotOpenArtifactOrigin(t *testing.T) {
	srv := capabilityServer(t)
	artifact, err := previewutil.ArtifactFileURL(srv.URL, "tp-1", "index.html")
	if err != nil {
		t.Fatal(err)
	}
	au, _ := url.Parse(artifact)
	workspace := strings.Split(previewHostFor(t, srv, "tp-1"), ".")
	forged := strings.Split(au.Host, ".")
	forged[len(forged)-2] = workspace[len(workspace)-2]
	if _, status := getOnHost(t, srv, strings.Join(forged, ".")); status == http.StatusOK {
		t.Fatal("a workspace-origin capability opened the artifact origin")
	}
}

func TestPreviewCapability_LegacyIDKeyedPathsNeedTheToken(t *testing.T) {
	srv := capabilityServer(t)
	for _, p := range []string{"/api/v1/sessions/tp-1/preview/files/index.html", "/api/v1/sessions/tp-1/preview/artifacts/index.html"} {
		resp, err := srv.Client().Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("tokenless GET %s = %d, want 401", p, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/files/index.html", http.NoBody)
	req.Header.Set("X-VO-Token", capabilityTestToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("tokened GET = %d, want 200", resp.StatusCode)
	}
}

func TestPreviewFileURL_OpensAFileWithoutChangingTheSessionPreview(t *testing.T) {
	workspace := t.TempDir()
	for file, text := range map[string]string{"dist/index.html": "app", "src/foo.ts": "export {}", ".env": "S=1", "docs/page.html": "<script>fetch(location.origin)</script>"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(workspace, file)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspace, file), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	svc := newFakeSessionService()
	s := svc.sessions["tp-1"]
	s.Metadata = domain.SessionMetadata{WorkspacePath: workspace, PreviewURL: "dist/index.html"}
	svc.sessions["tp-1"] = s
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, nil, nil,
		httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{DaemonToken: capabilityTestToken}))
	t.Cleanup(srv.Close)

	mint := func(p string) (string, int) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/file-url?path="+url.QueryEscape(p), http.NoBody)
		req.Header.Set("X-VO-Token", capabilityTestToken)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out struct {
			URL string `json:"url"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return out.URL, resp.StatusCode
	}
	fileURL, status := mint("src/foo.ts")
	if status != http.StatusOK || fileURL == "" {
		t.Fatalf("mint = %d %q", status, fileURL)
	}
	body, status, _ := doPreviewOriginRequest(t, srv, fileURL, "/src/foo.ts")
	if status != http.StatusOK || string(body) != "export {}" {
		t.Fatalf("files origin = %d %q, want the file served from the workspace root", status, body)
	}
	// The stored preview and its origin are untouched.
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "dist/index.html" {
		t.Fatalf("session preview changed to %q", got)
	}
	appURL, _ := previewutil.FileURL(srv.URL, "tp-1", "dist/index.html")
	if body, status, _ := doPreviewOriginRequest(t, srv, appURL, "/"); status != http.StatusOK || string(body) != "app" {
		t.Fatalf("app preview origin = %d %q, want it still rooted at dist/", status, body)
	}
	// A page opened from a chat link keeps an opaque origin: it must not be able
	// to read the rest of the workspace from its own origin.
	htmlURL, _ := mint("docs/page.html")
	_, status, headers := doPreviewOriginRequest(t, srv, htmlURL, "/docs/page.html")
	if csp := headers.Get("Content-Security-Policy"); status != http.StatusOK || !strings.HasPrefix(csp, "sandbox") || strings.Contains(csp, "allow-same-origin") {
		t.Fatalf("files-origin html = %d CSP %q, want a sandbox CSP without allow-same-origin", status, csp)
	}
	if headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("files-origin html is missing nosniff")
	}
	// Secrets are neither minted nor served; an id-only files host is refused.
	if u, status := mint(".env"); status != http.StatusNotFound || u != "" {
		t.Fatalf("mint .env = %d %q, want 404", status, u)
	}
	if body, status, _ := doPreviewOriginRequest(t, srv, fileURL, "/.env"); status != http.StatusNotFound || string(body) == "S=1" {
		t.Fatalf("files origin .env = %d %q", status, body)
	}
	u, _ := url.Parse(fileURL)
	labels := strings.Split(u.Host, ".")
	idOnly := strings.Join(append(labels[:len(labels)-2:len(labels)-2], labels[len(labels)-1]), ".")
	if _, status := getOnHost(t, srv, idOnly); status == http.StatusOK {
		t.Fatal("id-only files host was served")
	}
}
