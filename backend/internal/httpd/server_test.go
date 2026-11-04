package httpd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthProbes(t *testing.T) {
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/json; charset=utf-8" {
			t.Errorf("GET %s Content-Type = %q, want JSON", path, ct)
		}
	}
}

func TestHealthProbesIncludeDaemonIdentity(t *testing.T) {
	router := newTestRouter(config.Config{StartupWorkingDirectory: "/startup"}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	wantExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wantCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		var body struct {
			ExecutablePath          string `json:"executablePath"`
			WorkingDirectory        string `json:"workingDirectory"`
			StartupWorkingDirectory string `json:"startupWorkingDirectory"`
			Build                   struct {
				Version string  `json:"version"`
				Commit  *string `json:"commit"`
				Dirty   *bool   `json:"dirty"`
			} `json:"build"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if body.Build.Version == "" || body.Build.Commit == nil || body.Build.Dirty == nil {
			t.Errorf("GET %s build = %+v, want version, commit and dirty", path, body.Build)
		}
		if body.ExecutablePath != wantExe {
			t.Errorf("GET %s executablePath = %q, want %q", path, body.ExecutablePath, wantExe)
		}
		if body.WorkingDirectory != wantCWD {
			t.Errorf("GET %s workingDirectory = %q, want %q", path, body.WorkingDirectory, wantCWD)
		}
		if body.StartupWorkingDirectory != "/startup" {
			t.Errorf("GET %s startupWorkingDirectory = %q, want /startup", path, body.StartupWorkingDirectory)
		}
	}
}

func TestHealthProbesIncludeAppImageIdentity(t *testing.T) {
	client := &http.Client{Timeout: 2 * time.Second}

	t.Run("reports TALLPINE_APPIMAGE when set", func(t *testing.T) {
		t.Setenv("TALLPINE_APPIMAGE", "/home/user/Apps/tallpine.AppImage")
		router := newTestRouter(config.Config{}, discardLogger(), nil)
		srv := httptest.NewServer(router)
		defer srv.Close()

		for _, path := range []string{"/healthz", "/readyz"} {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			var body struct {
				AppImagePath string `json:"appImagePath"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if body.AppImagePath != "/home/user/Apps/tallpine.AppImage" {
				t.Errorf("GET %s appImagePath = %q, want the TALLPINE_APPIMAGE value", path, body.AppImagePath)
			}
		}
	})

	t.Run("omits appImagePath when TALLPINE_APPIMAGE is unset", func(t *testing.T) {
		t.Setenv("TALLPINE_APPIMAGE", "")
		router := newTestRouter(config.Config{}, discardLogger(), nil)
		srv := httptest.NewServer(router)
		defer srv.Close()

		for _, path := range []string{"/healthz", "/readyz"} {
			resp, err := client.Get(srv.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			var body map[string]any
			if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
				t.Fatalf("decode %s: %v", path, err)
			}
			if _, ok := body["appImagePath"]; ok {
				t.Errorf("GET %s appImagePath present, want omitted", path)
			}
		}
	})
}

// TestServerLifecycle exercises the full Run loop: bind an ephemeral port,
// publish running.json, serve a request, then cancel the context and confirm a
// clean shutdown that removes the handshake file.
func TestServerLifecycle(t *testing.T) {
	runPath := shortRunPath(t)
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0, // let the OS pick a free port — no conflict with a real daemon
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	// Wait for the handshake file to confirm the server is up.
	client, base := daemonClient(t, srv, 2*time.Second)
	waitForHealth(t, client, base)

	info, err := runfile.Read(runPath)
	if err != nil {
		t.Fatalf("read run-file: %v", err)
	}
	if info == nil {
		t.Fatal("run-file not written while server running")
		return
	}
	// The run-file must name a way in: the socket when bound (B176 keeps TCP
	// closed), otherwise the actual bound TCP port.
	if srv.sock != nil && info.Socket != srv.sockPath {
		t.Errorf("run-file socket = %q, want %q", info.Socket, srv.sockPath)
	}
	if srv.sock == nil && info.Port == 0 {
		t.Error("run-file recorded port 0; want the actual bound port")
	}

	cancel()

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on graceful shutdown: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown; want it removed")
	}
}

func TestServerRunWithReadyPublishesBeforeCallback(t *testing.T) {
	runPath := shortRunPath(t)
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() {
		runErr <- srv.RunWithReady(ctx, func() { close(ready) })
	}()

	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("ready callback was not called")
	}
	if info, err := runfile.Read(runPath); err != nil || info == nil {
		t.Fatalf("run-file unavailable in ready callback: info=%v err=%v", info, err)
	}
	client, base := daemonClient(t, srv, 2*time.Second)
	waitForHealth(t, client, base)

	cancel()
	if err := <-runErr; err != nil {
		t.Fatalf("RunWithReady returned error on graceful shutdown: %v", err)
	}
}

func TestServerShutdownEndpoint(t *testing.T) {
	runPath := shortRunPath(t)
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     runPath,
	}

	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	client, base := daemonClient(t, srv, 2*time.Second)
	waitForHealth(t, client, base)

	// Not http.Post: the shared DefaultTransport races the health poll's conn
	// return-to-pool and dials a spare that never carries a request; the server
	// then waits on that silent conn for the whole ShutdownTimeout (observed as
	// a flaky 5s failure under -race). DisableKeepAlives keeps this POST off
	// the shared pool entirely (daemonClient disables keep-alives).
	token, err := daemontoken.Read(daemontoken.Path(runPath))
	if err != nil {
		t.Fatalf("read daemon token: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/shutdown", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(daemontoken.Header, token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /shutdown = %d, want 202", resp.StatusCode)
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned error on shutdown endpoint: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after shutdown endpoint")
	}

	if after, _ := runfile.Read(runPath); after != nil {
		t.Error("run-file still present after shutdown endpoint; want it removed")
	}
}

// shortRunPath returns a run-file path under a short temp dir so the daemon
// socket fits sun_path on every platform. t.TempDir() is too long on macOS:
// the bind fails there and the TCP fallback hides socket-only bugs that only
// show on Linux (B176).
func shortRunPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "running.json")
}

// daemonClient returns a client and base URL that reach srv the way the
// desktop app does: over the socket, which with B176 is the only listener by
// default. Without a socket (Windows) the daemon keeps loopback TCP. The client
// never reuses connections, so a shutdown cannot wait on an idle pooled conn.
func daemonClient(t *testing.T, srv *Server, timeout time.Duration) (*http.Client, string) {
	t.Helper()
	base := "http://" + srv.Addr().String()
	if srv.sock == nil {
		if runtime.GOOS != "windows" {
			t.Fatalf("daemon socket not bound at %q; want a socket on %s", srv.sockPath, runtime.GOOS)
		}
		return &http.Client{Timeout: timeout, Transport: &http.Transport{DisableKeepAlives: true}}, base
	}
	tr := daemonsock.Transport(srv.sockPath)
	tr.DisableKeepAlives = true
	return &http.Client{Timeout: timeout, Transport: tr}, base
}

func waitForHealth(t *testing.T, client *http.Client, base string) {
	t.Helper()
	// Per-request timeout so a stalled connect or hung handshake doesn't park
	// the test for the full Go test timeout; the outer deadline only bounds
	// the polling loop, not any single GET.
	client = &http.Client{Timeout: 500 * time.Millisecond, Transport: client.Transport}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not become healthy within timeout")
}

// TestNewFallsBackOnPortConflict confirms that when the configured port is
// already held, the constructor binds an ephemeral port instead of failing, so
// the desktop supervisor never gets stuck on "daemon not ready".
func TestNewFallsBackOnPortConflict(t *testing.T) {
	// LoopbackTCP: the port fallback only matters when TCP is open (B176).
	cfg := config.Config{Host: "127.0.0.1", Port: 0, LoopbackTCP: true, RunFilePath: filepath.Join(t.TempDir(), "r.json")}

	first, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	defer first.listen.Close()

	// Request the exact port the first server took; the second server should
	// fall back to a different, ephemeral port rather than error out.
	conflict := config.Config{Host: "127.0.0.1", Port: first.boundPort(), LoopbackTCP: true, RunFilePath: cfg.RunFilePath}
	second, err := NewWithDeps(conflict, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New on an already-bound port = %v, want ephemeral fallback", err)
	}
	defer second.listen.Close()

	if second.boundPort() == first.boundPort() {
		t.Fatalf("second server bound the same port %d; want a fallback port", second.boundPort())
	}
	if second.boundPort() == 0 {
		t.Fatal("second server bound port 0; want a real fallback port")
	}
}

// An empty /shutdown keeps agents for the next daemon (restart, takeover);
// {"stopAgents":true} is a deliberate quit that stops them first.
func TestServerShutdownEndpointStopAgentsIsOptIn(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{"", false},
		{`{"stopAgents":false}`, false},
		{`{"stopAgents":true}`, true},
	} {
		runPath := shortRunPath(t)
		srv, err := NewWithDeps(config.Config{Host: "127.0.0.1", ShutdownTimeout: 5 * time.Second, RunFilePath: runPath}, discardLogger(), nil, APIDeps{})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		runErr := make(chan error, 1)
		go func() { runErr <- srv.Run(context.Background()) }()
		client, base := daemonClient(t, srv, 2*time.Second)
		waitForHealth(t, client, base)
		token, err := daemontoken.Read(daemontoken.Path(runPath))
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(http.MethodPost, base+"/shutdown", strings.NewReader(tc.body))
		req.Header.Set(daemontoken.Header, token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /shutdown %q: %v", tc.body, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST /shutdown %q = %d", tc.body, resp.StatusCode)
		}
		select {
		case <-runErr:
		case <-time.After(10 * time.Second):
			t.Fatal("Run did not return")
		}
		if got := srv.StopAgentsOnExit(); got != tc.want {
			t.Errorf("body %q: StopAgentsOnExit = %v, want %v", tc.body, got, tc.want)
		}
	}
}

func TestShutdownRouteRejectsMalformedBody(t *testing.T) {
	var shutdowns, stops int
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{}, ControlDeps{
		RequestShutdown:      func() { shutdowns++ },
		StopAgentsOnShutdown: func() { stops++ },
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/shutdown", strings.NewReader(`{"stopAgents":`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	time.Sleep(30 * time.Millisecond)
	if w.Code != http.StatusBadRequest || shutdowns != 0 || stops != 0 {
		t.Fatalf("malformed body: status=%d shutdowns=%d stops=%d", w.Code, shutdowns, stops)
	}
}

func TestServerShutdownEndsOpenSSEStream(t *testing.T) {
	cfg := config.Config{
		Host:               "127.0.0.1",
		Port:               0,
		ShutdownTimeout:    10 * time.Second,
		RunFilePath:        shortRunPath(t),
		DaemonAuthDisabled: true,
	}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Same shape as the notification stream: ends only on the request context.
	connected := make(chan struct{})
	srv.http.Handler = streamShutdownMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(connected)
		<-r.Context().Done()
	}))
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	client, base := daemonClient(t, srv, 0)
	resp, err := client.Get(base + "/api/v1/notifications/stream")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	<-connected

	srv.requestShutdown()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not return promptly with an open SSE stream")
	}
}

func TestServerShutdownLetsInFlightRequestsDrain(t *testing.T) {
	cfg := config.Config{
		Host:               "127.0.0.1",
		Port:               0,
		ShutdownTimeout:    10 * time.Second,
		RunFilePath:        shortRunPath(t),
		DaemonAuthDisabled: true,
	}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	ctxErr := make(chan error, 1)
	// Wrapped by the same middleware the router applies to every request.
	srv.http.Handler = streamShutdownMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		ctxErr <- r.Context().Err()
		w.WriteHeader(http.StatusOK)
	}))
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(context.Background()) }()

	status := make(chan int, 1)
	client, base := daemonClient(t, srv, 0)
	go func() {
		resp, err := client.Get(base + "/api/v1/sessions")
		if err != nil {
			status <- -1
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-started
	srv.requestShutdown()
	// Let the shutdown hooks run while the request is still in flight.
	time.Sleep(300 * time.Millisecond)
	close(release)
	if err := <-ctxErr; err != nil {
		t.Fatalf("in-flight request ctx cancelled during Shutdown: %v", err)
	}
	if got := <-status; got != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want 200", got)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
}
