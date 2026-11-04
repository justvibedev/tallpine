package controllers_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/previewserver"
	browsersvc "tallpine.eu/tallpine/backend/internal/service/browser"
)

func TestSessionsAPI_ManagedPreviewStartsExactApplicationAndPersistsTarget(t *testing.T) {
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.WorkspacePath = t.TempDir()
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{
		State:         previewserver.StateReady,
		Configuration: "web",
		TargetKind:    previewserver.TargetApp,
		URL:           "http://127.0.0.1:43123/",
		Port:          43123,
		Logs:          []string{"ready"},
	}}
	srv := newSessionTestServerWithPreview(t, svc, managed)

	body, status, _ := doRequest(
		t,
		srv,
		http.MethodPost,
		"/api/v1/sessions/tp-1/preview/server",
		`{"configuration":"web"}`,
	)
	if status != http.StatusOK || !containsAll(body, `"state":"ready"`, `"configuration":"web"`, `"targetKind":"app"`) {
		t.Fatalf("start managed preview = %d body=%s", status, body)
	}
	if managed.startName != "web" || managed.startWorkspace != session.Metadata.WorkspacePath {
		t.Fatalf("start args = name %q workspace %q", managed.startName, managed.startWorkspace)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != managed.status.URL {
		t.Fatalf("persisted preview URL = %q, want %q", got, managed.status.URL)
	}
}

func TestSessionsAPI_ManagedPreviewReceivesProjectEnv(t *testing.T) {
	svc := newFakeSessionService()
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetAPI}}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: svc, Projects: previewProjectManager{env: map[string]string{"PROJECT_TOKEN": "preview-value"}},
		PreviewServer: managed, SessionCapabilities: allowSessionCapability{},
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	_, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`)
	if status != http.StatusOK || managed.startEnv["PROJECT_TOKEN"] != "preview-value" {
		t.Fatalf("preview project env missing, status=%d", status)
	}
}

func TestSessionsAPI_ManagedPreviewCompressesUncompressedText(t *testing.T) {
	module := strings.Repeat("export const value = 'remote preview module';\n", 100)
	oversized := strings.Repeat("a", 4<<20+1) // one byte over the 4 MiB cap
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/streamed.js": // no announced length: chunked, possibly a stream
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = io.WriteString(w, module)
			return
		case "/oversized.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Content-Length", strconv.Itoa(len(oversized)))
			_, _ = io.WriteString(w, oversized)
			return
		case "/ranged.js":
			w.Header().Set("Content-Type", "text/javascript")
			http.ServeContent(w, r, "ranged.js", time.Time{}, strings.NewReader(module))
			return
		case "/mod.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("ETag", `"v1"`)
		case "/tiny.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Content-Length", "11")
			_, _ = io.WriteString(w, "export {};\n")
			return
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
		case "/encoded.js":
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("Content-Encoding", "br")
		case "/image.png":
			w.Header().Set("Content-Type", "image/png")
		}
		// Announced, as a Vite module's is.
		w.Header().Set("Content-Length", strconv.Itoa(len(module)))
		_, _ = io.WriteString(w, module)
	}))
	t.Cleanup(app.Close)
	parsed, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(parsed.Port())
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = app.URL + "/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetApp, URL: session.Metadata.PreviewURL, Port: port}}
	srv := newSessionTestServerWithPreview(t, svc, managed)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	get := func(path, acceptEncoding, byteRange string) (*http.Response, []byte) {
		t.Helper()
		request, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/app"+path, nil)
		request.Header.Set("Authorization", "Bearer host-password")
		if acceptEncoding != "" {
			request.Header.Set("Accept-Encoding", acceptEncoding)
		}
		if byteRange != "" {
			request.Header.Set("Range", byteRange)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		body, _ := io.ReadAll(response.Body)
		return response, body
	}

	for range 2 { // the second reuses the pooled compressor
		response, body := get("/mod.js", "gzip, deflate, br, zstd", "")
		if response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("ETag") != `W/"v1"` {
			t.Fatalf("module headers = %v, want gzip with a weak ETag", response.Header)
		}
		// Its headers carry the gzip's own length: the module was compressed
		// whole before the client read a byte, so no compressor waits on it.
		if response.ContentLength != int64(len(body)) {
			t.Fatalf("gzip module announced %d bytes and sent %d", response.ContentLength, len(body))
		}
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		decoded, _ := io.ReadAll(reader)
		if string(decoded) != module || len(body) >= len(module)/4 {
			t.Fatalf("decoded %d of %d bytes from %d compressed", len(decoded), len(module), len(body))
		}
	}
	for _, tc := range []struct{ path, acceptEncoding, byteRange, encoding string }{
		{"/mod.js", "", "", ""},
		{"/mod.js", "gzip;q=0, br", "", ""},
		{"/mod.js", "gzip;q=0.0, br", "", ""},
		{"/mod.js", "br, GZIP; Q=0.000", "", ""},
		{"/mod.js", "br, gzip;q=0.5", "", "gzip"},
		{"/mod.js", "gzip", "bytes=0-2047", ""}, // range offsets count uncompressed bytes
		{"/ranged.js", "gzip", "bytes=0-2047", ""},
		{"/streamed.js", "gzip", "", ""},
		{"/tiny.js", "gzip", "", ""},
		{"/oversized.js", "gzip", "", ""},
		{"/events", "gzip", "", ""},
		{"/encoded.js", "gzip", "", "br"},
		{"/image.png", "gzip", "", ""},
	} {
		if response, _ := get(tc.path, tc.acceptEncoding, tc.byteRange); response.Header.Get("Content-Encoding") != tc.encoding {
			t.Errorf("%s with Accept-Encoding %q, Range %q: Content-Encoding %q, want %q", tc.path, tc.acceptEncoding, tc.byteRange, response.Header.Get("Content-Encoding"), tc.encoding)
		}
	}
	if response, body := get("/ranged.js", "gzip", "bytes=0-2047"); response.StatusCode != http.StatusPartialContent || string(body) != module[:2048] {
		t.Errorf("ranged module = %d with %d bytes, want 206 with the first 2048 as sent", response.StatusCode, len(body))
	}
}

// A streamed body (SSR, RSC payloads, token streams) announces no length. Each
// write reaches a client that accepts gzip as the dev server makes it, not
// once gzip has 64 KiB or the end.
func TestSessionsAPI_ManagedPreviewStreamsWithoutHoldingBack(t *testing.T) {
	release := make(chan struct{})
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", r.URL.Query().Get("type"))
		_, _ = io.WriteString(w, strings.Repeat("first ", 400)+"\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(app.Close)
	t.Cleanup(func() { close(release) })
	parsed, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(parsed.Port())
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = app.URL + "/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetApp, URL: session.Metadata.PreviewURL, Port: port}}
	srv := newSessionTestServerWithPreview(t, svc, managed)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	for _, contentType := range []string{"text/html; charset=utf-8", "text/x-component", "text/plain; charset=utf-8", "application/x-ndjson", "text/event-stream"} {
		request, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/app/stream?type="+url.QueryEscape(contentType), nil)
		request.Header.Set("Authorization", "Bearer host-password")
		request.Header.Set("Accept-Encoding", "gzip")
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		first := make(chan string, 1)
		go func() {
			line, _ := bufio.NewReader(response.Body).ReadString('\n')
			first <- line
		}()
		select {
		case line := <-first:
			if !strings.HasPrefix(line, "first ") || response.Header.Get("Content-Encoding") != "" {
				t.Errorf("%s: first line %.20q with Content-Encoding %q, want it as written", contentType, line, response.Header.Get("Content-Encoding"))
			}
		case <-time.After(5 * time.Second):
			t.Errorf("%s: first write held back while the stream stays open", contentType)
		}
		_ = response.Body.Close()
	}
}

// A dev server that dies mid-module must fail the request. A response that
// ends cleanly would hand the browser a shorter module, which it caches.
func TestSessionsAPI_ManagedPreviewAbortsACutModule(t *testing.T) {
	random := make([]byte, 256<<10)
	_, _ = rand.Read(random)
	module := hex.EncodeToString(random) // 512 KiB announced, half of it sent
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Length", strconv.Itoa(len(module)))
		_, _ = io.WriteString(w, module[:len(module)/2])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // the dev server dies
	}))
	t.Cleanup(app.Close)
	parsed, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(parsed.Port())
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = app.URL + "/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetApp, URL: session.Metadata.PreviewURL, Port: port}}
	srv := newSessionTestServerWithPreview(t, svc, managed)
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	request, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/app/mod.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := client.Do(request)
	var body []byte
	if err == nil {
		body, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
	}
	if err == nil {
		t.Errorf("the cut module arrived as a whole %d-byte response", len(body))
	}
	if reader, gzipErr := gzip.NewReader(bytes.NewReader(body)); gzipErr == nil {
		if decoded, gzipErr := io.ReadAll(reader); gzipErr == nil {
			t.Errorf("the cut module's gzip stream ends cleanly after %d of %d bytes", len(decoded), len(module))
		}
	}
}

// ReverseProxy flushes every write of a body it has no length for, and gzip
// writes a few hundred bytes at a time: a compressed module must still go out
// in large writes, not as a thousand small frames.
func TestSessionsAPI_ManagedPreviewWritesCompressedModulesInLargePieces(t *testing.T) {
	var source strings.Builder
	for i := 0; source.Len() < 1<<20; i++ {
		source.WriteString("export function handle" + strconv.Itoa(i) + "(event) { return dispatch(\"" + strconv.Itoa(i*7919) + "\", event); }\n")
	}
	module := source.String()
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Content-Length", strconv.Itoa(len(module)))
		_, _ = io.WriteString(w, module)
	}))
	t.Cleanup(app.Close)
	parsed, _ := url.Parse(app.URL)
	port, _ := strconv.Atoi(parsed.Port())
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = app.URL + "/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetApp, URL: session.Metadata.PreviewURL, Port: port}}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{
		Sessions: svc, PreviewServer: managed, SessionCapabilities: allowSessionCapability{},
	}, httpd.ControlDeps{})
	request := loopbackRequest(http.MethodGet, "/api/v1/sessions/tp-1/preview/app/mod.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	recorder := &countingRecorder{ResponseRecorder: httptest.NewRecorder()}
	router.ServeHTTP(recorder, request)

	compressed := recorder.Body.Len()
	reader, err := gzip.NewReader(bytes.NewReader(recorder.Body.Bytes()))
	if err != nil || recorder.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("response %d with headers %v, want a gzip module (%v)", recorder.Code, recorder.Header(), err)
	}
	if decoded, err := io.ReadAll(reader); err != nil || string(decoded) != module {
		t.Fatalf("decoded %d of %d bytes: %v", len(decoded), len(module), err)
	}
	if most := compressed/(16<<10) + 2; recorder.writes > most || recorder.flushes > most {
		t.Errorf("%d compressed bytes went out in %d writes and %d flushes, want at most %d of each", compressed, recorder.writes, recorder.flushes, most)
	}
}

func TestSessionsAPI_ManagedPreviewAppProxy(t *testing.T) {
	type observed struct{ path, host, authorization, origin, cookie, relay string }
	var seenMu sync.Mutex
	var seen observed
	readSeen := func() observed {
		seenMu.Lock()
		defer seenMu.Unlock()
		return seen
	}
	var app *httptest.Server
	app = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		seen = observed{
			path: r.URL.RequestURI(), host: r.Host,
			authorization: r.Header.Get("Authorization"), origin: r.Header.Get("Origin"), cookie: r.Header.Get("Cookie"),
			relay: r.Header.Get("X-AO-Preview-App-Authorization") + r.Header.Get("X-AO-Preview-App-Origin"),
		}
		seenMu.Unlock()
		if r.URL.Path == "/ws" {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept websocket: %v", err)
				return
			}
			defer func() { _ = conn.CloseNow() }()
			kind, message, err := conn.Read(r.Context())
			if err == nil {
				err = conn.Write(r.Context(), kind, message)
			}
			if err != nil {
				t.Errorf("echo websocket: %v", err)
			}
			return
		}
		if r.URL.Path == "/redirect" {
			w.Header().Set("Location", app.URL+"/app/next")
			w.WriteHeader(http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "managed app")
	}))
	t.Cleanup(app.Close)
	parsed, err := url.Parse(app.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil {
		t.Fatal(err)
	}
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = app.URL + "/app/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{State: previewserver.StateReady, TargetKind: previewserver.TargetApp, URL: session.Metadata.PreviewURL, Port: port}}
	srv := newSessionTestServerWithPreview(t, svc, managed)
	request, err := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/sessions/tp-1/preview/app/app/?q=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer host-password")
	request.Header.Set("Cookie", "ao_conn=host-password; app_session=ok")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	first := readSeen()
	if response.StatusCode != http.StatusOK || first.path != "/app/?q=1" || first.host != parsed.Host || first.authorization != "" || first.origin != "" || first.cookie != "app_session=ok" || first.relay != "" {
		t.Fatalf("app proxy: status=%d observed=%+v", response.StatusCode, first)
	}
	// Turbopack names chunks after scoped packages; "%40swc" must reach the app as sent, not as "%2540swc".
	if _, status, _ := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/app/_next/static/chunks/0i4a_%40swc_helpers.js", ""); status != http.StatusOK || readSeen().path != "/_next/static/chunks/0i4a_%40swc_helpers.js" {
		t.Fatalf("escaped app path: status=%d observed=%+v", status, readSeen())
	}
	request, err = http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/tp-1/preview/app/submit", strings.NewReader("data"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer host-password")
	request.Header.Set("Origin", "app://renderer")
	request.Header.Set("X-AO-Preview-App-Authorization", "Basic app-token")
	request.Header.Set("X-AO-Preview-App-Origin", "http://ao-preview-client.localhost:4321")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	posted := readSeen()
	if response.StatusCode != http.StatusOK || posted.authorization != "Basic app-token" || posted.origin != app.URL || posted.relay != "" {
		t.Fatalf("app headers: status=%d observed=%+v", response.StatusCode, posted)
	}

	redirectClient := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	redirect, err := redirectClient.Get(srv.URL + "/api/v1/sessions/tp-1/preview/app/redirect")
	if err != nil {
		t.Fatal(err)
	}
	_ = redirect.Body.Close()
	if got := redirect.Header.Get("Location"); got != "/app/next" {
		t.Fatalf("redirect Location = %q", got)
	}

	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/sessions/tp-1/preview/app/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization":                  []string{"Bearer host-password"},
			"Origin":                         []string{"app://renderer"},
			"X-Ao-Preview-App-Authorization": []string{"Basic ws-token"},
			"X-Ao-Preview-App-Origin":        []string{"http://ao-preview-client.localhost:4321"},
		},
	})
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(context.Background(), websocket.MessageText, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, message, err := conn.Read(context.Background())
	if err != nil || string(message) != "hello" {
		t.Fatalf("websocket echo = %q, %v", message, err)
	}
	upgraded := readSeen()
	if upgraded.authorization != "Basic ws-token" || upgraded.origin != app.URL || upgraded.relay != "" {
		t.Fatalf("websocket app headers: %+v", upgraded)
	}

	managed.status.State = previewserver.StateStopped
	body, status, headers := doRequest(t, srv, http.MethodGet, "/api/v1/sessions/tp-1/preview/app/", "")
	assertErrorCode(t, body, status, http.StatusConflict, "PREVIEW_MANAGED_REQUIRED")
	if headers.Get("X-AO-Preview-Managed-Required") != "1" {
		t.Fatal("missing managed-preview guidance marker")
	}
}

func TestSessionsAPI_ManagedPreviewRequiresOwningCapability(t *testing.T) {
	svc := newFakeSessionService()
	managed := &fakeManagedPreviewServer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{
		Sessions:            svc,
		PreviewServer:       managed,
		SessionCapabilities: denySessionCapability{},
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`)
	assertErrorCode(t, body, status, http.StatusForbidden, "PREVIEW_CAPABILITY_INVALID")
	if managed.startName != "" {
		t.Fatal("preview process started without a valid capability")
	}
}

func TestSessionsAPI_ShellPreviewCapabilityCannotAutomateBrowser(t *testing.T) {
	authority := browsersvc.NewAuthority()
	workerToken, workerVerifier, err := authority.Issue("tp-1")
	if err != nil {
		t.Fatal(err)
	}
	shellToken, _, err := authority.Issue("tp-1")
	if err != nil {
		t.Fatal(err)
	}
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.BrowserCapabilityVerifier = workerVerifier
	svc.sessions["tp-1"] = session
	shell := &fixedShellPreviewCapability{token: shellToken}
	managed := &fakeManagedPreviewServer{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{
		Sessions: svc, PreviewServer: managed, SessionCapabilities: authority,
		ShellPreviewCapabilities: shell,
		Browser:                  browsersvc.New(svc, nil, authority),
	}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	request := func(method, path, body, header, token string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(header, token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		responseBody, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode, string(responseBody)
	}
	previewPath := "/api/v1/sessions/tp-1/preview/server"
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""}, {http.MethodPost, `{}`}, {http.MethodDelete, ""},
	} {
		status, body := request(tc.method, previewPath, tc.body, "X-AO-Preview-Capability", shellToken)
		if status != http.StatusOK {
			t.Fatalf("shell %s preview = %d: %s", tc.method, status, body)
		}
	}
	status, body := request(http.MethodGet, previewPath, "", "X-AO-Browser-Capability", workerToken)
	if status != http.StatusOK {
		t.Fatalf("worker preview = %d: %s", status, body)
	}
	for _, tc := range []struct{ method, path, body, header string }{
		{http.MethodGet, "/api/v1/browser/status?sessionId=tp-1", "", "X-AO-Preview-Capability"},
		{http.MethodGet, "/api/v1/browser/status?sessionId=tp-1", "", "X-AO-Browser-Capability"},
		{http.MethodPost, "/api/v1/browser/commands", `{"sessionId":"tp-1","action":"snapshot"}`, "X-AO-Browser-Capability"},
	} {
		status, body := request(tc.method, tc.path, tc.body, tc.header, shellToken)
		if status != http.StatusForbidden || !strings.Contains(body, "BROWSER_CAPABILITY_INVALID") {
			t.Fatalf("shell token reached browser %s %s = %d: %s", tc.method, tc.path, status, body)
		}
	}
	shell.revoked = true
	status, body = request(http.MethodGet, previewPath, "", "X-AO-Preview-Capability", shellToken)
	if status != http.StatusForbidden || !strings.Contains(body, "PREVIEW_CAPABILITY_INVALID") {
		t.Fatalf("revoked shell preview = %d: %s", status, body)
	}
}

func TestSessionsAPI_APIManagedPreviewDoesNotTakeOverBrowser(t *testing.T) {
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.WorkspacePath = t.TempDir()
	session.Metadata.PreviewURL = "http://127.0.0.1:4173/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{
		State:         previewserver.StateReady,
		Configuration: "api",
		TargetKind:    previewserver.TargetAPI,
		URL:           "http://127.0.0.1:8080/health",
		Port:          8080,
		Logs:          []string{},
	}}
	srv := newSessionTestServerWithPreview(t, svc, managed)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/preview/server", `{}`)
	if status != http.StatusOK {
		t.Fatalf("start API preview = %d body=%s", status, body)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://127.0.0.1:4173/" {
		t.Fatalf("API server replaced browser target with %q", got)
	}
}

func TestSessionsAPI_StopManagedPreviewPreservesExplicitFileTarget(t *testing.T) {
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = "http://tp-1.preview.localhost:3101/README.md"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{
		State:      previewserver.StateReady,
		TargetKind: previewserver.TargetApp,
		URL:        "http://127.0.0.1:4173/",
		Logs:       []string{},
	}}
	srv := newSessionTestServerWithPreview(t, svc, managed)

	body, status, _ := doRequest(t, srv, http.MethodDelete, "/api/v1/sessions/tp-1/preview/server", "")
	if status != http.StatusOK || !containsAll(body, `"state":"stopped"`) {
		t.Fatalf("stop managed preview = %d body=%s", status, body)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != session.Metadata.PreviewURL {
		t.Fatalf("explicit file target was cleared: %q", got)
	}
}

func TestSessionsAPI_StopManagedPreviewPreservesTargetChangedDuringStop(t *testing.T) {
	svc := newFakeSessionService()
	session := svc.sessions["tp-1"]
	session.Metadata.PreviewURL = "http://127.0.0.1:4173/"
	svc.sessions["tp-1"] = session
	managed := &fakeManagedPreviewServer{status: previewserver.Status{
		State:      previewserver.StateReady,
		TargetKind: previewserver.TargetApp,
		URL:        session.Metadata.PreviewURL,
		Logs:       []string{},
	}}
	managed.onStop = func() {
		current := svc.sessions["tp-1"]
		current.Metadata.PreviewURL = "http://127.0.0.1:5173/"
		svc.sessions["tp-1"] = current
	}
	srv := newSessionTestServerWithPreview(t, svc, managed)

	body, status, _ := doRequest(t, srv, http.MethodDelete, "/api/v1/sessions/tp-1/preview/server", "")
	if status != http.StatusOK || !containsAll(body, `"state":"stopped"`) {
		t.Fatalf("stop managed preview = %d body=%s", status, body)
	}
	if got := svc.sessions["tp-1"].Metadata.PreviewURL; got != "http://127.0.0.1:5173/" {
		t.Fatalf("target changed during stop was cleared: %q", got)
	}
}

func TestSessionsAPI_KillLeavesPreviewTeardownToSessionLifecycle(t *testing.T) {
	svc := newFakeSessionService()
	managed := &fakeManagedPreviewServer{}
	srv := newSessionTestServerWithPreview(t, svc, managed)

	body, status, _ := doRequest(t, srv, http.MethodPost, "/api/v1/sessions/tp-1/kill", "")
	if status != http.StatusOK {
		t.Fatalf("kill = %d body=%s", status, body)
	}
	if managed.stopCalls != 0 {
		t.Fatalf("controller duplicated managed preview teardown: stop calls = %d", managed.stopCalls)
	}
}
