//go:build darwin || linux

package peeradmit

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/runtime/tmux"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/testenv"
)

type tmuxRoots struct {
	rt     *tmux.Runtime
	handle ports.RuntimeHandle
}

func (r tmuxRoots) AgentRoots(ctx context.Context, _ Credential) ([]int, error) {
	return r.rt.ProcessRootPIDs(ctx, r.handle)
}

func (tmuxRoots) ShellRoots(context.Context) ([]int, error) { return nil, nil }

func TestRealTmuxPaneClassifiedAcrossDaemonRestart(t *testing.T) {
	binary, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux unavailable")
	}
	root, err := os.MkdirTemp(testenv.ShortBaseDir(), "pa-tmux-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("TMUX", "")
	t.Setenv("TALLPINE_TMUX_SOCKET_NAME", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("PS1", "$ ")
	const socket = "peeradmit-fixture"
	rt := tmux.New(tmux.Options{Binary: binary, LegacyBinary: binary, SocketName: socket, Shell: "/bin/sh", Timeout: 5 * time.Second})
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_ = exec.CommandContext(cleanup, binary, "-L", socket, "kill-server").Run() //nolint:gosec // only this test's private socket
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	create := func(id string) ports.RuntimeHandle {
		handle, err := rt.Create(ctx, ports.RuntimeConfig{SessionID: domain.SessionID(id), WorkspacePath: t.TempDir(), Argv: []string{"/bin/sh"}})
		if err != nil {
			t.Fatal(err)
		}
		return handle
	}
	own, other := create("own"), create("other")
	roots := tmuxRoots{rt: rt, handle: own}
	send := func(handle ports.RuntimeHandle, path string) {
		t.Helper()
		line := strings.Join(append(helperEnv(path, CredentialSession), helperRoleEnv+"="+roleClient), " ") + " " + os.Args[0]
		if err := rt.SendMessage(ctx, handle, line); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(root, "s", "daemon.sock")
	first := realChecker(t, roots)
	server := startSocketServer(t, path, first, nil)
	send(own, path)
	waitForAdmitted(t, first, ClassAgent, 1)

	// A daemon restart keeps tmux and throws away every cache.
	server.stop()
	second := realChecker(t, roots)
	startSocketServer(t, path, second, nil)
	send(own, path)
	waitForAdmitted(t, second, ClassAgent, 1)

	// A child of another session holding this session's token.
	send(other, path)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		flush(t, second)
		if records := second.Report().WouldRefuse; len(records) > 0 {
			if records[0].Reason != ReasonScopedTokenOutside {
				t.Fatalf("another session's pane: %+v", records[0])
			}
			if got := second.Report().Admitted[ClassAgent]; got != 1 {
				t.Fatalf("admitted = %d, want 1 (the other pane must not count)", got)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	report := second.Report()
	t.Fatalf("another session's pane was never classified: %+v", report)
}

func waitForAdmitted(t *testing.T, c *Checker, class Class, want int64) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		flush(t, c)
		report := c.Report()
		if report.Admitted[class] >= want {
			return
		}
		if len(report.WouldRefuse) > 0 {
			t.Fatalf("pane child refused: %+v", report.WouldRefuse)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("pane child never classified as %s: %+v", class, c.Report())
}
