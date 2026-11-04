//go:build darwin || linux

package httpd

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// freeLoopbackPort returns a port nothing listens on right now.
func freeLoopbackPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// startServer runs a real Server on a short temp run-file path and returns it
// with its run-file path; the server stops at test cleanup.
func startServer(t *testing.T, cfg config.Config) (*Server, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "st") // short: sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	cfg.Host, cfg.ShutdownTimeout = "127.0.0.1", 5*time.Second
	cfg.RunFilePath = filepath.Join(dir, "running.json")
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithReady(ctx, func() { close(ready) }) }()
	<-ready
	t.Cleanup(func() {
		cancel()
		if err := <-runErr; err != nil {
			t.Error(err)
		}
	})
	return srv, cfg.RunFilePath
}

func healthz(t *testing.T, client *http.Client, base string) int {
	t.Helper()
	resp, err := client.Get(base + "/healthz")
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// B176: with the socket up, the daemon opens no loopback TCP port by default.
// running.json still names the configured port: it is the origin the desktop
// app serves daemon URLs on, over the socket.
func TestServerClosesLoopbackTCPByDefault(t *testing.T) {
	port := freeLoopbackPort(t)
	srv, runPath := startServer(t, config.Config{Port: port})

	if srv.ListensTCP() {
		t.Fatal("ListensTCP() = true, want the TCP listener closed by default")
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("a TCP connection to 127.0.0.1:%d succeeded; the daemon must not listen there", port)
	}
	info, err := runfile.Read(runPath)
	if err != nil || info == nil || info.Port != port || info.Socket != daemonsock.Path(runPath) {
		t.Fatalf("running.json = %+v, %v; want port %d and the socket", info, err, port)
	}
	if got := srv.Addr().String(); got != "127.0.0.1:"+strconv.Itoa(port) {
		t.Fatalf("Addr() = %s, want the configured origin", got)
	}
	client := &http.Client{Transport: daemonsock.Transport(info.Socket), Timeout: 5 * time.Second}
	if code := healthz(t, client, "http://127.0.0.1:"+strconv.Itoa(port)); code != http.StatusOK {
		t.Fatalf("healthz over the socket = %d, want 200", code)
	}
}

// TALLPINE_LOOPBACK_TCP=1 keeps the TCP listener for clients that cannot dial
// a Unix socket, beside the socket.
func TestServerKeepsLoopbackTCPWhenEnabled(t *testing.T) {
	port := freeLoopbackPort(t)
	srv, runPath := startServer(t, config.Config{Port: port, LoopbackTCP: true})

	if !srv.ListensTCP() {
		t.Fatal("ListensTCP() = false with LoopbackTCP set")
	}
	tcp := &http.Client{Transport: daemonsock.LoopbackTransport(), Timeout: 5 * time.Second}
	if code := healthz(t, tcp, "http://127.0.0.1:"+strconv.Itoa(port)); code != http.StatusOK {
		t.Fatalf("healthz over TCP = %d, want 200", code)
	}
	if _, err := os.Lstat(daemonsock.Path(runPath)); err != nil {
		t.Fatalf("socket missing beside TCP: %v", err)
	}
}

// Without a run-file there is no socket, so the daemon must stay reachable
// over TCP even though LoopbackTCP is off.
func TestServerKeepsTCPWithoutSocket(t *testing.T) {
	srv, err := NewWithDeps(config.Config{Host: "127.0.0.1", Port: 0, DaemonAuthDisabled: true, ShutdownTimeout: time.Second}, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.listen.Close() }()
	if !srv.ListensTCP() {
		t.Fatal("no socket and no TCP: the daemon would be unreachable")
	}
}
