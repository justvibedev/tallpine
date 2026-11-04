//go:build darwin || linux

package cli

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/httpd"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// B170: when running.json advertises the socket, the CLI (and the hooks,
// which are CLI subcommands) reach the real router over it with the token,
// never touching the TCP port (here a dead one).
func TestCLIUsesDaemonSocketFromRunFile(t *testing.T) {
	cfg := setConfigEnv(t)
	token, err := daemontoken.Ensure(daemontoken.Path(cfg.runFile))
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "cs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := daemonsock.Path(filepath.Join(dir, "running.json"))
	ln, err := daemonsock.Listen(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: httpd.NewRouterWithControl(config.Config{}, slog.New(slog.DiscardHandler), nil, httpd.APIDeps{}, httpd.ControlDeps{
			DaemonToken:     token,
			RequestShutdown: func() {},
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 1, Socket: sock, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	c := &commandContext{deps: Deps{HTTPClient: &http.Client{Timeout: 2 * time.Second}, ProcessAlive: func(int) bool { return true }}}

	// No project service is wired: reaching the route yields 501, so any
	// apiResponseError other than 401 proves the socket + token path works.
	err = c.getJSON(context.Background(), "projects", &struct{}{})
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.StatusCode == http.StatusUnauthorized {
		t.Fatalf("CLI over the daemon socket: err = %v, want a routed (non-401) API response", err)
	}
	if err := c.requestShutdown(context.Background(), sock, 1, cfg.runFile, false); err != nil {
		t.Fatalf("/shutdown over the socket: %v", err)
	}
	if _, err := c.readProbe(context.Background(), sock, 1, "healthz"); err != nil {
		t.Fatalf("healthz over the socket: %v", err)
	}
}

// Review on #472: one transport per socket, so keep-alive connections are reused.
func TestDaemonEndpointReusesSocketTransport(t *testing.T) {
	c := &commandContext{deps: Deps{HTTPClient: &http.Client{}}}
	_, a := c.daemonEndpoint("/x/a.sock", 1, 0)
	_, b := c.daemonEndpoint("/x/a.sock", 1, time.Second)
	_, other := c.daemonEndpoint("/x/b.sock", 1, 0)
	if a.Transport != b.Transport {
		t.Fatal("same socket got a new transport per call")
	}
	if a.Transport == other.Transport {
		t.Fatal("different sockets share a transport")
	}
}

// B176: over the socket the CLI still names the daemon's loopback origin in
// Host. The daemon builds render and preview URLs from it, and with TCP closed
// that origin is what the desktop app serves them on; "localhost" (no port)
// would hand out URLs nothing serves.
func TestCLISocketRequestsNameLoopbackOrigin(t *testing.T) {
	cfg := setConfigEnv(t)
	dir, err := os.MkdirTemp("", "ch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := daemonsock.Path(filepath.Join(dir, "running.json"))
	ln, err := daemonsock.Listen(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	hosts := make(chan string, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hosts <- r.Host
			_, _ = w.Write([]byte(`{}`))
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: 3101, Socket: sock, StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	c := &commandContext{deps: Deps{HTTPClient: &http.Client{Timeout: 2 * time.Second}, ProcessAlive: func(int) bool { return true }}}
	_ = c.getJSON(context.Background(), "projects", &struct{}{})
	select {
	case got := <-hosts:
		if got != "127.0.0.1:3101" {
			t.Fatalf("Host over the socket = %q, want 127.0.0.1:3101", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the CLI request never reached the socket")
	}
}
