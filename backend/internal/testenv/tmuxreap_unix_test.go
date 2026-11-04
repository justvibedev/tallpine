//go:build !windows

package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shortTmp returns a fresh dir under tmuxBaseDir. t.TempDir() sits under the
// session's long $TMPDIR, which overflows the 104-byte unix socket path limit
// once tmux appends tmux-<uid>/<name>.
func shortTmp(t *testing.T) string {
	t.Helper()
	_ = os.MkdirAll(tmuxBaseDir, 0o700)
	dir, err := os.MkdirTemp(tmuxBaseDir, "tp-tt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startTmuxIn starts a detached tmux server whose socket lives under dir.
func startTmuxIn(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	cmd := exec.Command("tmux", "new-session", "-d", "-s", "b72", "sleep 300")
	cmd.Env = append(os.Environ(), "TMUX_TMPDIR="+dir, "TMUX=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, out)
	}
	socks, _ := filepath.Glob(filepath.Join(dir, "tmux-*", "*"))
	if len(socks) != 1 {
		t.Fatalf("sockets = %v", socks)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socks[0], "kill-server").Run() })
	return socks[0]
}

// serverPID returns the pid of the server on sock. It is probed by pid, not
// by socket: a server whose socket file was deleted is still running.
func serverPID(t *testing.T, sock string) int {
	t.Helper()
	out, err := exec.Command("tmux", "-S", sock, "display-message", "-p", "#{pid}").Output()
	if err != nil {
		t.Fatalf("server pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("server pid %q: %v", out, err)
	}
	return pid
}

func waitPIDDown(pid int) bool {
	for i := 0; i < 50; i++ {
		if !pidAlive(pid) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func TestReapStaleTmuxServersKillsDeadOwnerServer(t *testing.T) {
	skipIfFenced(t)
	root := shortTmp(t)
	dead := filepath.Join(root, "tp-tmux-dead")
	live := filepath.Join(root, "tp-tmux-live")
	mkOwner(t, dead, "999999999")
	mkOwner(t, live, "1") // pid 1 is always alive
	deadPID := serverPID(t, startTmuxIn(t, dead))
	livePID := serverPID(t, startTmuxIn(t, live))

	reapStaleTmuxServers(root)

	if !waitPIDDown(deadPID) {
		t.Fatal("dead-owner tmux server survived reapStaleTmuxServers")
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("dead-owner dir not removed: %v", err)
	}
	if !pidAlive(livePID) {
		t.Fatal("live-owner tmux server was killed")
	}
}

func TestKillTmuxServersStopsServerBeforeDirRemoval(t *testing.T) {
	skipIfFenced(t)
	dir := shortTmp(t)
	pid := serverPID(t, startTmuxIn(t, dir))
	killTmuxServers(dir)
	if !waitPIDDown(pid) {
		t.Fatal("tmux server survived killTmuxServers")
	}
}

func TestTmuxSocketKillsServerAtCleanup(t *testing.T) {
	skipIfFenced(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	var name string
	var pid int
	t.Run("inner", func(t *testing.T) {
		name = TmuxSocket(t)
		if out, err := exec.Command("tmux", "-L", name, "new-session", "-d", "-s", "b72", "sleep 300").CombinedOutput(); err != nil {
			t.Fatalf("start tmux: %v: %s", err, out)
		}
		out, err := exec.Command("tmux", "-L", name, "display-message", "-p", "#{pid}").Output()
		if err != nil {
			t.Fatalf("server pid: %v", err)
		}
		pid, _ = strconv.Atoi(strings.TrimSpace(string(out)))
	})
	if !waitPIDDown(pid) {
		_ = exec.Command("tmux", "-L", name, "kill-server").Run()
		t.Fatal("server on the TmuxSocket survived t.Cleanup")
	}
}

// The production reaper root must find dirs Setup creates even when $TMPDIR
// points elsewhere (macOS), or stale servers are never reaped.
func TestReaperRootFindsSetupDirWhenTMPDIRDiffers(t *testing.T) {
	skipIfFenced(t)
	t.Setenv("TMPDIR", t.TempDir())
	dir, err := mkTmuxDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte("999999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	pid := serverPID(t, startTmuxIn(t, dir))

	reapStaleTmuxServers(tmuxReapRoot())

	if !waitPIDDown(pid) {
		t.Fatal("server in Setup-created dir survived the production reaper root")
	}
}
