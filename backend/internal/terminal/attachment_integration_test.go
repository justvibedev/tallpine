//go:build !windows

package terminal

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/tmux"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

// tmuxWait bounds a step that spans real tmux process spawns (IsAlive, Attach,
// the post-Destroy liveness probe, plus reattach backoff). Under a loaded
// machine each spawn can take seconds, so a fixed 3-5s deadline flaked. eventually
// returns as soon as the condition holds, so this only caps a genuine hang.
const tmuxWait = 60 * time.Second

// TestAttachmentStreamsRealTmuxPane attaches a real PTY to a real tmux session
// and asserts output streams back, then that killing the session stops the
// attachment without a re-attach storm. Skipped when tmux is unavailable.
func TestAttachmentStreamsRealTmuxPane(t *testing.T) {
	requireTmux(t)

	name := "tallpine-term-it-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{SocketName: testenv.TmuxSocket(t), Timeout: 10 * time.Second})
	// The marker is quoted so it ends in a real newline; see the legacy-socket
	// tests in the tmux package for why a prompt redraw would erase it.
	handle := createReadyShell(t, rt, name)

	a, got := runAttachment(t, name, handle, rt)

	// Type a unique marker and expect it echoed back through the PTY.
	eventually(t, tmuxWait, func() bool { return a.writeLeased([]byte("echo TALLPINE_MARKER_42\n"), nil) == nil })
	eventually(t, tmuxWait, func() bool { return strings.Contains(got.string(), "TALLPINE_MARKER_42") })

	// Kill the session: the attachment must observe it as gone and not re-attach.
	destroyAndAwaitExit(t, rt, handle, a)
}

// TestAttachmentExitsOnDestroyUnderDetachOnDestroyOff is the regression for
// issue #4223. A user tmux.conf with `set -g detach-on-destroy off` also
// governs Tallpine's tmux server, so destroying a Tallpine session used to reparent Tallpine's
// attach client onto one of the user's own sessions instead of exiting: the
// embedded terminal kept streaming and typed keystrokes leaked into that
// session. The test starts an isolated tmux server with that config, adds a
// decoy "user" session for the client to hop onto, and asserts the attachment
// still observes the destroy as an exit.
func TestAttachmentExitsOnDestroyUnderDetachOnDestroyOff(t *testing.T) {
	systemTmux := requireTmux(t)

	// tmux reads its config only when the server starts, so route every
	// invocation (Create, Attach, Destroy) through a wrapper that passes -f on
	// a private socket: the user's real server and config stay untouched.
	dir := t.TempDir()
	conf := filepath.Join(dir, "tmux.conf")
	if err := os.WriteFile(conf, []byte("set -g detach-on-destroy off\n"), 0o600); err != nil {
		t.Fatalf("write tmux.conf: %v", err)
	}
	wrapper := filepath.Join(dir, "tmux")
	script := "#!/bin/sh\nexec '" + systemTmux + "' -f '" + conf + "' \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}
	socket := "tallpine-term-dod-it-" + strconv.Itoa(os.Getpid())
	t.Cleanup(func() { _ = exec.Command(wrapper, "-L", socket, "kill-server").Run() })

	// The decoy is the user's own session: with detach-on-destroy off, tmux
	// would move Tallpine's client here when the Tallpine session dies.
	decoy := "user-session-" + strconv.Itoa(os.Getpid())
	if out, err := exec.Command(wrapper, "-L", socket, "new-session", "-d", "-s", decoy, "sh", "-c", "sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("create decoy session: %v: %s", err, out)
	}

	name := "tallpine-term-dod-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{Binary: wrapper, LegacyBinary: wrapper, SocketName: socket, Timeout: 10 * time.Second})
	handle := createReadyShell(t, rt, name)

	a, got := runAttachment(t, name, handle, rt)
	eventually(t, tmuxWait, func() bool { return strings.Contains(got.string(), "TALLPINE_READY") })

	// Destroying the Tallpine session must end the attach client rather than hop it
	// onto the decoy, so the attachment sees an exit and stops.
	destroyAndAwaitExit(t, rt, handle, a)
}

// TestAttachmentReattachAdoptsNewSize is the end-to-end regression for the
// stale-size desync: client A holds the session at one grid, detaches, and
// client B immediately attaches at a different grid (the frontend's
// remount/reconnect flow). B's tmux client must adopt B's size, not A's.
func TestAttachmentReattachAdoptsNewSize(t *testing.T) {
	requireTmux(t)

	name := "tallpine-term-size-it-" + strconv.Itoa(os.Getpid())
	rt := tmux.New(tmux.Options{SocketName: testenv.TmuxSocket(t), Timeout: 10 * time.Second})
	handle := createReadyShell(t, rt, name)

	attachAt := func(rows, cols uint16) (*attachment, *safeBytes, <-chan struct{}, context.CancelFunc) {
		var got safeBytes
		opened := make(chan struct{})
		a := newAttachment(name, handle, rt, func() { close(opened) }, got.add, nil, testLogger())
		if err := a.resize(rows, cols); err != nil {
			t.Fatalf("record size: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		go a.run(ctx)
		return a, &got, opened, cancel
	}

	// Client A at 115x37: wait for the pane shell, then detach.
	a, _, openedA, cancelA := attachAt(37, 115)
	select {
	case <-openedA:
	case <-time.After(10 * time.Second):
		t.Fatal("client A did not attach")
	}
	a.close()
	cancelA()

	// Client B re-attaches immediately at 148x40. The inner pane must see B's
	// grid (tmux may shave a row/col; assert cols land near 148 and far from 115).
	b, gotB, openedB, cancelB := attachAt(40, 148)
	defer cancelB()
	defer b.close()
	select {
	case <-openedB:
	case <-time.After(10 * time.Second):
		t.Fatal("client B did not attach")
	}

	// Drive the reattached shell until it reports its width. We RESEND the probe
	// each iteration: onOpen means the stream accepts input, not that the inner
	// `sh -i` is already at a prompt reading stdin after the reattach, so an early
	// keystroke can be dropped; retrying covers that. Real tmux + shell output is
	// also slow under -race on CI, hence the long deadline. On timeout we dump
	// exactly what the pane produced so the failure is self-explaining (e.g. the
	// probe echoed but never executed, or stty errored).
	var captured string
	gotWidth := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_ = b.writeLeased([]byte("echo SIZE:$(stty size)\n"), nil)
		time.Sleep(250 * time.Millisecond)
		captured = gotB.string()
		i := strings.LastIndex(captured, "SIZE:")
		if i < 0 {
			continue
		}
		fields := strings.Fields(strings.TrimPrefix(captured[i:], "SIZE:"))
		if len(fields) < 2 {
			continue
		}
		cols, err := strconv.Atoi(strings.TrimFunc(fields[1], func(r rune) bool { return r < '0' || r > '9' }))
		if err != nil {
			continue
		}
		if cols > 130 { // B's 148 minus any tmux chrome; a stale A-layout reports <=115
			gotWidth = true
			break
		}
	}
	if !gotWidth {
		t.Fatalf("reattached pane never reported B's width (cols>130) within 30s; captured:\n%q", captured)
	}
}
