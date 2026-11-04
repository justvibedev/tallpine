//go:build !windows

package terminal

import (
	"context"
	"os/exec"
	"testing"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/tmux"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// requireTmux skips the test without tmux and sets the TERM tmux needs to
// attach a client (it refuses with "open terminal failed: terminal does not
// support clear" otherwise; the daemon sets a default TERM in production, CI
// runners have none). It returns the tmux binary path.
func requireTmux(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	t.Setenv("TERM", "xterm-256color")
	return bin
}

// createReadyShell starts a tmux session whose pane prints TALLPINE_READY and
// then runs an interactive sh, and destroys it on cleanup. The marker is
// quoted so it ends in a real newline (a prompt redraw would erase it).
func createReadyShell(t *testing.T, rt *tmux.Runtime, name string) ports.RuntimeHandle {
	t.Helper()
	handle, err := rt.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     domain.SessionID(name),
		WorkspacePath: t.TempDir(),
		Argv:          []string{"sh", "-lc", "printf 'TALLPINE_READY\\n'; exec sh -i"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = rt.Destroy(context.Background(), handle) })
	return handle
}

// runAttachment runs an attachment over handle until the test ends and returns
// it with the bytes it has received so far.
func runAttachment(t *testing.T, name string, handle ports.RuntimeHandle, rt *tmux.Runtime) (*attachment, *safeBytes) {
	t.Helper()
	var got safeBytes
	a := newAttachment(name, handle, rt, nil, got.add, nil, testLogger())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go a.run(ctx)
	return a, &got
}

// destroyAndAwaitExit destroys the session and waits for the attachment to
// observe it as gone.
func destroyAndAwaitExit(t *testing.T, rt *tmux.Runtime, handle ports.RuntimeHandle, a *attachment) {
	t.Helper()
	if err := rt.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	eventually(t, tmuxWait, func() bool { return a.isExited() })
}
