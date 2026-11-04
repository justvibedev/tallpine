//go:build darwin || linux

package httpd

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

// B343: a connected-but-silent client must not hold shutdown for Go's 5s
// StateNew window, while a request already in flight still drains.
func TestShutdownIgnoresSilentConnButDrainsInFlight(t *testing.T) {
	dir, err := os.MkdirTemp("", "ss")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	runPath := filepath.Join(dir, "running.json")
	srv, err := NewWithDeps(config.Config{Host: "127.0.0.1", Port: 0, ShutdownTimeout: 8 * time.Second, RunFilePath: runPath}, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatal(err)
	}
	inHandler := make(chan struct{})
	release := make(chan struct{})
	inner := srv.http.Handler
	srv.http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(inHandler)
			<-release
			w.WriteHeader(http.StatusTeapot)
			return
		}
		inner.ServeHTTP(w, r)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	runErr := make(chan error, 1)
	go func() { runErr <- srv.RunWithReady(ctx, func() { close(ready) }) }()
	<-ready
	sock := daemonsock.Path(runPath)

	silent, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	client := &http.Client{Transport: daemonsock.Transport(sock), Timeout: 10 * time.Second}
	status := make(chan int, 1)
	go func() {
		resp, err := client.Get("http://localhost/slow")
		if err != nil {
			status <- -1
			return
		}
		_ = resp.Body.Close()
		status <- resp.StatusCode
	}()
	<-inHandler

	start := time.Now()
	cancel()
	// The silent conn must be closed promptly by the server.
	_ = silent.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := silent.Read(make([]byte, 1)); err == nil {
		t.Fatal("silent conn got data, want it closed")
	} else if ne := net.Error(nil); errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("silent conn still open 2s into shutdown")
	}
	select {
	case err := <-runErr:
		t.Fatalf("shutdown finished while a request was in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if code := <-status; code != http.StatusTeapot {
		t.Fatalf("in-flight request = %d, want 418 (drained)", code)
	}
	if err := <-runErr; err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("shutdown took %v with a silent conn", d)
	}
}
