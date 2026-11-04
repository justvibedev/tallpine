//go:build darwin || linux

package daemonsock

import (
	"context"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortDir keeps the socket under sun_path; t.TempDir() is too deep on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func serve(t *testing.T, ln net.Listener) {
	t.Helper()
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "ok "+r.Host)
		}),
		ReadHeaderTimeout: time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
}

// TestListenPermsAndRealConnect binds a real socket (not a socketpair),
// checks the 0700 dir / 0600 socket, and does an HTTP round trip through it
// so the LOCAL_PEERCRED / SO_PEERCRED path must answer "same uid".
func TestListenPermsAndRealConnect(t *testing.T) {
	root := shortDir(t)
	// A pre-existing, too-open dir must be tightened.
	if err := os.MkdirAll(filepath.Join(root, DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	path := Path(filepath.Join(root, "running.json"))
	ln, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	serve(t, ln)

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Fatalf("dir mode = %o, want 700", got)
	}
	si, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if si.Mode()&fs.ModeSocket == 0 {
		t.Fatalf("%s is not a socket", path)
	}
	if got := si.Mode().Perm(); got != 0o600 {
		t.Fatalf("socket mode = %o, want 600", got)
	}

	client := &http.Client{Transport: Transport(path), Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost/x")
	if err != nil {
		t.Fatalf("request over socket: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ok localhost" {
		t.Fatalf("body = %q", body)
	}

	// Real peer credentials of a real connect: must be our uid.
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if uid, err := peerUID(conn); err != nil || uid != os.Getuid() {
		t.Fatalf("peerUID = %d, %v; want %d", uid, err, os.Getuid())
	}

	// Close unlinks the socket.
	_ = ln.Close()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket not unlinked on close: %v", err)
	}
}

// TestPeerUIDMismatchRefused: a peer whose uid is not the daemon's gets its
// connection closed without a response (we fake the "other user" by changing
// the expected uid, since tests cannot run as a second user).
func TestPeerUIDMismatchRefused(t *testing.T) {
	path := Path(filepath.Join(shortDir(t), "running.json"))
	ln, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*peerListener).uid = os.Getuid() + 1
	serve(t, ln)

	client := &http.Client{Transport: Transport(path), Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost/x", http.NoBody)
	resp, err := client.Do(req)
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("foreign-uid peer got a response; want the connection refused")
	}
}

func TestListenRemovesStaleSocketButNotFiles(t *testing.T) {
	path := Path(filepath.Join(shortDir(t), "running.json"))
	ln, err := Listen(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a crash: keep the socket file, drop the listener.
	ln.(*peerListener).Listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	ln2, err := Listen(path, nil)
	if err != nil {
		t.Fatalf("rebind over stale socket: %v", err)
	}
	_ = ln2.Close()

	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(path, nil); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("Listen over a regular file: err = %v", err)
	}
}

func TestListenRejectsLongPath(t *testing.T) {
	long := "/" + strings.Repeat("a", 120) + "/daemon.sock"
	if _, err := Listen(long, nil); err == nil {
		t.Fatal("want ErrPathTooLong")
	}
}
