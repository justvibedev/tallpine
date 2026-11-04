//go:build darwin || linux

package httpd

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// B170: the daemon serves the same router on a 0600 user-only socket,
// advertises it in running.json, keeps the bearer check on it, and unlinks
// it on shutdown.
func TestServerServesUserOnlySocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "ss") // short: sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	runPath := filepath.Join(dir, "running.json")
	srv, err := NewWithDeps(config.Config{Host: "127.0.0.1", Port: 0, ShutdownTimeout: 5 * time.Second, RunFilePath: runPath}, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithReady(ctx, func() { close(ready) }) }()
	<-ready

	sock := daemonsock.Path(runPath)
	info, err := runfile.Read(runPath)
	if err != nil || info == nil || info.Socket != sock {
		t.Fatalf("running.json socket = %+v, %v; want %s", info, err, sock)
	}
	fi, err := os.Lstat(sock)
	if err != nil || fi.Mode()&fs.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket stat = %v, %v; want a 0600 socket", fi, err)
	}
	token, err := os.ReadFile(daemontoken.Path(runPath))
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: daemonsock.Transport(sock), Timeout: 5 * time.Second}
	get := func(withToken bool) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/api/v1/projects", http.NoBody)
		if withToken {
			req.Header.Set(daemontoken.Header, strings.TrimSpace(string(token)))
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request over socket: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := get(false); code != http.StatusUnauthorized {
		t.Fatalf("socket without token: %d, want 401 (bearer kept on the socket)", code)
	}
	if code := get(true); code == http.StatusUnauthorized {
		t.Fatalf("socket with token: %d", code)
	}

	// Close the client's pooled conns first: a speculative extra dial that never
	// sent a request stays StateNew server-side, and Shutdown waits for such a
	// conn until it is 5s old (B342).
	client.CloseIdleConnections()
	cancel()
	if err := <-runErr; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket left behind after shutdown: %v", err)
	}
}
