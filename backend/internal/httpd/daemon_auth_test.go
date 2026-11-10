package httpd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

const testDaemonToken = "test-daemon-token-0123456789abcdefghijklmnop"

func newAuthedTestServer(t *testing.T, termMgr *terminal.Manager) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewRouterWithControl(config.Config{AllowedOrigins: []string{"app://renderer"}}, discardLogger(), termMgr, APIDeps{}, ControlDeps{
		DaemonToken:     testDaemonToken,
		DaemonTokenPath: "/home/u/.tallpine/daemon.token",
	}))
	t.Cleanup(srv.Close)
	return srv
}

func doAuthRequest(t *testing.T, method, url string, headers http.Header) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestDaemonAuthAPIRoute(t *testing.T) {
	srv := newAuthedTestServer(t, nil)
	for _, tc := range []struct {
		name    string
		path    string
		headers http.Header
		want401 bool
	}{
		{name: "missing token", path: "/api/v1/projects", want401: true},
		{name: "wrong token", path: "/api/v1/projects", headers: http.Header{daemontoken.Header: {"nope"}}, want401: true},
		{name: "token prefix", path: "/api/v1/projects", headers: http.Header{daemontoken.Header: {testDaemonToken[:10]}}, want401: true},
		{name: "duplicate headers", path: "/api/v1/projects", headers: http.Header{daemontoken.Header: {testDaemonToken, testDaemonToken}}, want401: true},
		{name: "bearer is not the daemon token", path: "/api/v1/projects", headers: http.Header{"Authorization": {"Bearer " + testDaemonToken}}, want401: true},
		{name: "query string is never accepted", path: "/api/v1/projects?token=" + testDaemonToken + "&" + daemontoken.Header + "=" + testDaemonToken, want401: true},
		{name: "shutdown control route", path: "/shutdown", want401: true},
		{name: "correct token", path: "/api/v1/projects", headers: http.Header{daemontoken.Header: {testDaemonToken}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.path == "/shutdown" {
				method = http.MethodPost
			}
			resp := doAuthRequest(t, method, srv.URL+tc.path, tc.headers)
			if got := resp.StatusCode == http.StatusUnauthorized; got != tc.want401 {
				t.Fatalf("status = %d, want401 = %v", resp.StatusCode, tc.want401)
			}
			if !tc.want401 {
				return
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "DAEMON_TOKEN_REQUIRED" || !strings.Contains(body.Message, daemontoken.Header) || !strings.Contains(body.Message, "/home/u/.tallpine/daemon.token") {
				t.Fatalf("401 body = %+v", body)
			}
		})
	}
}

func TestDaemonAuthWebSocketMux(t *testing.T) {
	mgr := terminal.NewManager(&muxOriginSource{}, nil, discardLogger())
	defer mgr.Close()
	srv := newAuthedTestServer(t, mgr)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/mux"
	for _, tc := range []struct {
		name    string
		headers http.Header
		ok      bool
	}{
		{name: "missing token"},
		{name: "wrong token", headers: http.Header{daemontoken.Header: {"nope"}}},
		{name: "token as subprotocol is not accepted", headers: http.Header{"Sec-WebSocket-Protocol": {testDaemonToken}}},
		{name: "correct token", headers: http.Header{daemontoken.Header: {testDaemonToken}}, ok: true},
		{name: "correct token from the renderer origin", headers: http.Header{daemontoken.Header: {testDaemonToken}, "Origin": {"app://renderer"}}, ok: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: tc.headers})
			if tc.ok {
				if err != nil {
					t.Fatalf("dial with token: %v", err)
				}
				_ = c.Close(websocket.StatusNormalClosure, "")
				return
			}
			if err == nil {
				_ = c.Close(websocket.StatusNormalClosure, "")
				t.Fatal("websocket upgraded without a valid token")
			}
			if resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("response=%v err=%v; want 401", resp, err)
			}
		})
	}
}

func TestDaemonAuthExemptions(t *testing.T) {
	srv := newAuthedTestServer(t, nil)
	for _, path := range []string{"/healthz", "/readyz"} {
		if resp := doAuthRequest(t, http.MethodGet, srv.URL+path, nil); resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s without token = %d, want 200", path, resp.StatusCode)
		}
	}
	preflight := doAuthRequest(t, http.MethodOptions, srv.URL+"/api/v1/projects", http.Header{
		"Origin":                        {"app://renderer"},
		"Access-Control-Request-Method": {"POST"},
	})
	if preflight.StatusCode != http.StatusNoContent {
		t.Fatalf("renderer preflight = %d, want 204", preflight.StatusCode)
	}
	hostile := doAuthRequest(t, http.MethodOptions, srv.URL+"/api/v1/projects", http.Header{
		"Origin":                        {"http://evil.example"},
		"Access-Control-Request-Method": {"POST"},
	})
	if hostile.StatusCode != http.StatusForbidden {
		t.Fatalf("hostile preflight = %d, want CORS 403", hostile.StatusCode)
	}
	if resp := doAuthRequest(t, http.MethodOptions, srv.URL+"/api/v1/projects", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bare OPTIONS without token = %d, want 401", resp.StatusCode)
	}

	for _, tc := range []struct {
		method, path, host string
		exempt             bool
	}{
		{http.MethodGet, "/index.html", capHost(t, "tp-1") + ":3101", true},
		{http.MethodGet, "/index.html", "ao-preview.mfxs2mi.localhost:3101", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/preview/files/index.html", "", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/preview/artifacts/index.html", "", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/renders/r1", "", true},
		{http.MethodPost, "/api/v1/sessions/mfxs2mi/activity/codewhale", "", false},
		{http.MethodPost, "/api/v1/sessions/mfxs2mi/renders/r1", "", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/renders/check", "", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/activity/codewhale", "", false},
		{http.MethodPost, "/api/v1/sessions/mfxs2mi/activity", "", false},
		{http.MethodGet, "/api/v1/sessions/mfxs2mi/conversation", "", false},
	} {
		req := httptest.NewRequest(tc.method, "http://127.0.0.1:3101"+tc.path, nil)
		if tc.host != "" {
			req.Host = tc.host
		}
		if got := daemonAuthExempt(req); got != tc.exempt {
			t.Errorf("daemonAuthExempt(%s %s host=%q) = %v, want %v", tc.method, tc.path, tc.host, got, tc.exempt)
		}
	}
}

func TestDaemonAuthLANRequestsUseBearerInstead(t *testing.T) {
	reached := false
	h := daemonAuthMiddleware(testDaemonToken, "", nil, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	req := httptest.NewRequest(http.MethodGet, "http://192.168.1.5:4100/api/v1/projects", nil)
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(requestscope.WithLAN(req.Context())))
	if !reached {
		t.Fatal("a LAN request (already bearer-authenticated) must not need the loopback token")
	}
}

func TestRouterWithoutTokenDoesNotEnforce(t *testing.T) {
	srv := httptest.NewServer(newTestRouter(config.Config{}, discardLogger(), nil))
	defer srv.Close()
	if resp := doAuthRequest(t, http.MethodGet, srv.URL+"/api/v1/projects", nil); resp.StatusCode == http.StatusUnauthorized {
		t.Fatal("a router built without ControlDeps.DaemonToken must not enforce")
	}
}

func TestNewWithDepsEnforcesTokenFromRunFileDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Host: "127.0.0.1", Port: 0, RunFilePath: filepath.Join(dir, "running.json")}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.closeListeners()
	tokenPath := filepath.Join(dir, daemontoken.FileName)
	info, err := os.Stat(tokenPath)
	if err != nil {
		t.Fatalf("token file not created next to running.json: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %o, want 600", info.Mode().Perm())
	}
	token, err := daemontoken.Read(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	serve := func(withToken string) int {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/projects", nil)
		if withToken != "" {
			req.Header.Set(daemontoken.Header, withToken)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := serve(""); code != http.StatusUnauthorized {
		t.Fatalf("no token = %d, want 401", code)
	}
	if code := serve(token); code == http.StatusUnauthorized {
		t.Fatal("token from the file was rejected")
	}

	// A token file deleted while the daemon runs is restored on the next
	// rejected request, with the same token live clients already hold.
	if err := os.Remove(tokenPath); err != nil {
		t.Fatal(err)
	}
	_ = serve("")
	if restored, err := daemontoken.Read(tokenPath); err != nil || restored != token {
		t.Fatalf("restored token = %q, %v; want the running daemon's token", restored, err)
	}
}

func TestNewWithDepsAuthOffEscapeHatch(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{Host: "127.0.0.1", Port: 0, RunFilePath: filepath.Join(dir, "running.json"), DaemonAuthDisabled: true}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.closeListeners()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/v1/projects", nil))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("TALLPINE_DAEMON_AUTH=off must disable enforcement")
	}
	if _, err := os.Stat(filepath.Join(dir, daemontoken.FileName)); !os.IsNotExist(err) {
		t.Fatalf("auth off should not create a token file: %v", err)
	}
}

func TestDaemonAuthHidesSecretsFromUnauthenticatedPreviewReads(t *testing.T) {
	srv := newAuthedTestServer(t, nil)
	for _, tc := range []struct {
		path, host string
		token      bool
		served     bool
	}{
		{path: "/index.html", host: capHost(t, "app-1"), served: true},
		{path: "/.well-known/assetlinks.json", host: capHost(t, "app-1"), served: true},
		// Ordinary dotfiles: the Browser tab opens workspace-file links
		// through this path without the token.
		{path: "/.github/workflows/ci.yml", host: capHost(t, "app-1"), served: true},
		{path: "/.claude/settings.json", host: capHost(t, "app-1"), served: true},
		{path: "/.storybook/main.ts", host: capHost(t, "app-1"), served: true},
		{path: "/.env.example", host: capHost(t, "app-1"), served: true},
		{path: "/.tallpine/attachments/a.png", host: capHost(t, "app-1"), served: true},
		// Credentials.
		{path: "/.env", host: capHost(t, "app-1")},
		{path: "/%2Eenv", host: capHost(t, "app-1")},
		{path: "/.ENV.local", host: capHost(t, "app-1")},
		{path: "/.git/config", host: capHost(t, "app-1")},
		{path: "/web/.npmrc", host: capHost(t, "app-1")},
		{path: "/web%5C.env", host: capHost(t, "app-1")},
		{path: "/.ssh/id_ed25519", host: capHost(t, "app-1")},
		{path: "/deploy/id_rsa", host: capHost(t, "app-1")},
		{path: "/certs/server.key", host: capHost(t, "app-1")},
		{path: "/.docker/config.json", host: capHost(t, "app-1")},
		{path: "/infra/terraform.tfstate", host: capHost(t, "app-1")},
		{path: "/.env.local", host: capArtifactHost(t, "app-1")},
		{path: "/api/v1/sessions/app-1/renders/r1", served: true},
		{path: "/index.html", host: capHost(t, "app-1"), served: true},
		{path: "/.github/workflows/ci.yml", host: capHost(t, "app-1"), served: true},
		{path: "/.git/config", host: capHost(t, "app-1")},
		{path: "/assets/.env.local", host: capHost(t, "app-1")},
		// The renderer and framed agent pages carry the token.
		{path: "/api/v1/sessions/app-1/preview/files/.env", token: true, served: true},
		{path: "/.git/config", host: capHost(t, "app-1"), token: true, served: true},
	} {
		req, err := http.NewRequest(http.MethodGet, srv.URL+tc.path, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}
		if tc.host != "" {
			req.Host = tc.host + srv.URL[strings.LastIndex(srv.URL, ":"):]
		}
		if tc.token {
			req.Header.Set(daemontoken.Header, testDaemonToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		// No session service is wired, so a request that passes auth gets the
		// route's own 404/501; only the secret-path refusal names
		// PREVIEW_FILE_NOT_FOUND before any handler runs.
		hidden := resp.StatusCode == http.StatusNotFound && strings.Contains(string(body), "PREVIEW_FILE_NOT_FOUND")
		if resp.StatusCode == http.StatusUnauthorized || hidden == tc.served {
			t.Errorf("GET %s%s (token=%v) = %d %s, want served=%v", tc.host, tc.path, tc.token, resp.StatusCode, body, tc.served)
		}
	}
}

// closeListeners releases the listeners of a server a test built but never
// ran. The loopback TCP listener is nil by default (B176), so close whichever
// of it and the Unix socket exist.
func (s *Server) closeListeners() {
	if s.listen != nil {
		_ = s.listen.Close()
	}
	if s.sock != nil {
		_ = s.sock.Close()
	}
}
