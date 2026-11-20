//go:build !windows

package conpty

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/hostsock"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// TestTerminatedHostSocketIsDefinitivelyGone is the B843 regression: a
// reviewer pty-host stopped by SIGTERM (a worker's `pkill -f <session>`) shuts
// down through Serve, and its hostsock listener deletes the socket file. The
// daemon still caches the old "unix:" address, so every probe hit
// `lstat ...sock: no such file or directory`. That error was classified as a
// transient probe failure, so the reviewer's Alive check failed every review
// trigger until a daemon restart. A vanished socket must instead read as a gone
// host, so the review engine destroys the stale handle and launches a fresh pane.
func TestTerminatedHostSocketIsDefinitivelyGone(t *testing.T) {
	isolateRegistry(t)
	ln, endpoint, err := hostsock.Listen(filepath.Join(t.TempDir(), hostsock.DirName))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	h := newHost(ServeConfig{SessionID: "review-w", Listener: ln, Token: "tok", PTY: newFakePTY(4242), Ring: NewRing()})
	go func() { done <- h.run(ctx) }()

	const id, launch = "review-w", "launch-1"
	rt := New(Options{})
	rt.sessions[id] = &hostSession{addr: hostAddr(endpoint, "tok"), pid: deadPID(), launchID: launch}
	handle := ports.RuntimeHandle{ID: id}
	ref := ports.SupervisedProcessRef{SessionID: id, LaunchID: launch}

	if alive, err := rt.IsChildAlive(context.Background(), handle); !alive || err != nil {
		t.Fatalf("IsChildAlive(serving host) = (%v, %v), want (true, nil)", alive, err)
	}

	// The SIGTERM handler in RunHost cancels Serve's context.
	cancel()
	<-done
	if _, err := os.Lstat(hostsock.Path(endpoint)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("socket after host shutdown: lstat err = %v, want not-exist", err)
	}

	if alive, err := rt.IsChildAlive(context.Background(), handle); alive || err != nil {
		t.Fatalf("IsChildAlive(vanished socket) = (%v, %v), want (false, nil)", alive, err)
	}
	if alive, err := rt.IsExactSupervisedProcessAlive(context.Background(), handle, ref); alive || err != nil {
		t.Fatalf("IsExactSupervisedProcessAlive(vanished socket) = (%v, %v), want (false, nil)", alive, err)
	}
	if alive, err := rt.IsAlive(context.Background(), handle); alive || err != nil {
		t.Fatalf("IsAlive(vanished socket) = (%v, %v), want (false, nil)", alive, err)
	}
	// The relaunch path destroys the stale handle before Create; that must
	// succeed and drop the cached address so the handle id can be reused.
	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy(vanished socket) = %v, want nil", err)
	}
	if _, cached := rt.sessions[id]; cached {
		t.Fatal("Destroy left the stale session cached")
	}
}

// A socket that never existed in a dir that does not exist is gone as well;
// a legacy TCP address keeps its refused-only rule.
func TestIsHostGoneMissingUnixPath(t *testing.T) {
	addr := hostAddr(hostsock.Scheme+filepath.Join(t.TempDir(), "absent", "x.sock"), "tok")
	if err := clientKill(addr); err != nil {
		t.Fatalf("clientKill(missing socket) = %v, want idempotent nil", err)
	}
	if _, alive, err := clientStatus(addr); alive || err != nil {
		t.Fatalf("clientStatus(missing socket) = (%v, %v), want (false, nil)", alive, err)
	}
	if isHostGone("127.0.0.1:1", fs.ErrNotExist) {
		t.Fatal("isHostGone treated a TCP not-exist error as gone")
	}
}
