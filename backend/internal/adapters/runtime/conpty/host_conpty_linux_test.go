//go:build linux

package conpty

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestLinuxPTYConnStreamsResizesAndReportsExit(t *testing.T) {
	runPTYConnStreamsResizesAndReportsExit(t, "Linux", func(c ptyConn) (*os.File, bool) {
		lc, ok := c.(*linuxPTYConn)
		if !ok {
			return nil, false
		}
		return lc.pty, true
	})
}

func TestLinuxDefaultSpawnHostEndToEnd(t *testing.T) { runDefaultSpawnHostEndToEnd(t) }

func TestLinuxPTYCloseReapsTermIgnoringProcessGroup(t *testing.T) {
	conn, leaderPID, leaderStartTime := startLinuxSession(t, "/bin/sh", `trap '' TERM; (trap '' TERM; printf 'child-ready\n'; sleep 30) & wait`)
	if !linuxSessionAlive(leaderPID, leaderStartTime) {
		t.Fatalf("session %d was not alive after launch", leaderPID)
	}
	waitForPTYOutput(t, conn, "child-ready", "waiting for process-group fixture readiness")

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	requireLinuxSessionGone(t, leaderPID, leaderStartTime, "session %d survived PTY close")
}

func TestLinuxPTYCloseReapsOrphanedBackgroundJobWhenLeaderExitsEarly(t *testing.T) {
	conn, leaderPID, leaderStartTime := startLinuxSession(t, "/bin/sh", `printf 'bg-ready\n'; (trap '' HUP; sleep 30) & sleep 0.05; exit 0`)

	waitForPTYOutput(t, conn, "bg-ready", "waiting for background job readiness")

	select {
	case <-conn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("leader process did not exit early as expected")
	}

	if !linuxSessionAlive(leaderPID, leaderStartTime) {
		t.Fatalf("session %d had no live background processes after leader exit", leaderPID)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	requireLinuxSessionGone(t, leaderPID, leaderStartTime, "orphaned background process in session %d survived PTY close")
}

func TestLinuxPTYCloseReapsDescendantWithOwnProcessGroup(t *testing.T) {
	conn, leaderPID, leaderStartTime := startLinuxSession(t, "/bin/bash", `set -m; (trap '' TERM HUP; printf 'pg-child-ready\n'; sleep 30) & wait`)

	waitForPTYOutput(t, conn, "pg-child-ready", "waiting for job-control descendant readiness")

	procs := linuxFindSessionProcesses(leaderPID, leaderStartTime)
	foundSeparatePGID := false
	for _, p := range procs {
		if p.pgrp != leaderPID {
			foundSeparatePGID = true
			break
		}
	}
	if !foundSeparatePGID {
		t.Fatalf("expected at least one process with distinct pgid in session %d, found: %+v", leaderPID, procs)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	requireLinuxSessionGone(t, leaderPID, leaderStartTime, "descendant with separate process group in session %d survived PTY close")
}

func TestLinuxPTYCloseDoesNotKillUnrelatedProcessAfterPIDReuse(t *testing.T) {
	// Start an independent dummy process representing an unrelated process running on the host.
	dummy := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := dummy.Start(); err != nil {
		t.Fatal(err)
	}
	dummyPID := dummy.Process.Pid
	t.Cleanup(func() {
		if dummy.Process != nil {
			_ = dummy.Process.Kill()
			_ = dummy.Wait()
		}
	})

	dummyInfo, err := readLinuxProcInfo(dummyPID)
	if err != nil {
		t.Fatalf("reading dummy proc info: %v", err)
	}

	// Create a simulated linuxPTYConn whose leaderPID matches dummyPID,
	// but whose recorded leaderStartTime is from an earlier epoch (simulating a recycled PID).
	conn := &linuxPTYConn{
		leaderPID:       dummyPID,
		leaderStartTime: dummyInfo.startTime - 1000, // mismatch: earlier time
		doneC:           make(chan struct{}),
	}
	close(conn.doneC)

	// Close() must detect the start-time mismatch and send NO signals to dummyPID.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the dummy process is still running and unharmed.
	if !pidAlive(dummyPID) {
		t.Fatalf("unrelated process %d was killed by Close() despite PID identity mismatch", dummyPID)
	}
}

func TestLinuxPTYCloseDelayedAfterFullSessionExit(t *testing.T) {
	conn, err := newConPTY(t.TempDir(), "/bin/sh", []string{
		"-c", `printf 'done\n'; exit 0`,
	}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatal(err)
	}
	linuxConn, ok := conn.(*linuxPTYConn)
	if !ok {
		t.Fatalf("connection type = %T", conn)
	}
	leaderPID := linuxConn.leaderPID

	waitForPTYOutput(t, conn, "done", "waiting for exit")

	select {
	case <-conn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("process did not exit")
	}

	// Ensure the leader process has exited
	deadline := time.Now().Add(2 * time.Second)
	for pidAlive(leaderPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// Delayed close on an already fully-dead session must succeed cleanly without error.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLinuxPTYCloseFailsClosedWhenLeaderStartTimeIsZero(t *testing.T) {
	// Start an independent dummy process representing an innocent process on the host.
	dummy := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := dummy.Start(); err != nil {
		t.Fatal(err)
	}
	dummyPID := dummy.Process.Pid
	t.Cleanup(func() {
		if dummy.Process != nil {
			_ = dummy.Process.Kill()
			_ = dummy.Wait()
		}
	})

	// Create a simulated linuxPTYConn whose leaderPID matches dummyPID,
	// but whose recorded leaderStartTime is 0 (simulating failed identity capture).
	conn := &linuxPTYConn{
		leaderPID:       dummyPID,
		leaderStartTime: 0, // identity unavailable: must fail closed
		doneC:           make(chan struct{}),
	}
	close(conn.doneC)

	// Close() must fail closed: send NO signals to dummyPID or its process group.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Verify the dummy process is still running and unharmed.
	if !pidAlive(dummyPID) {
		t.Fatalf("unrelated process %d was killed by Close() when leaderStartTime was zero", dummyPID)
	}
}

func TestLinuxRuntimeDestroyReapsTermIgnoringProcessTreeEndToEnd(t *testing.T) {
	isolateRegistry(t)
	runtime := New(Options{Spawner: defaultSpawnHost})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handle, err := runtime.Create(ctx, ports.RuntimeConfig{
		SessionID:     "term-reap-e2e",
		WorkspacePath: t.TempDir(),
		Argv: []string{
			"/bin/sh", "-c", `trap '' TERM; (trap '' TERM; printf 'term-trap-ready\n'; sleep 30) & wait`,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait for process readiness output.
	deadline := time.Now().Add(3 * time.Second)
	for {
		out, outErr := runtime.GetOutput(context.Background(), handle, 10)
		if outErr == nil && strings.Contains(out, "term-trap-ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for term-trap-ready output: %q (err: %v)", out, outErr)
		}
		time.Sleep(10 * time.Millisecond)
	}

	sess, err := runtime.resolveWithEvidence(context.Background(), handle.ID)
	if err != nil {
		t.Fatalf("resolve session: %v", err)
	}
	if sess == nil {
		t.Fatal("session not found in runtime")
		return
	}
	hostPID := sess.pid

	// Destroy the session through the runtime.
	if err := runtime.Destroy(context.Background(), handle); err != nil {
		t.Fatalf("Destroy: %v", err)
	}

	// Ensure pty-host is gone.
	if pidAlive(hostPID) {
		t.Fatalf("pty-host pid %d survived Destroy", hostPID)
	}
}

func TestLinuxPTYCloseDoesNotKillRecycledPIDDuringGracePeriod(t *testing.T) {
	// Start an independent innocent dummy process representing an unrelated process.
	dummy := exec.Command("/bin/sh", "-c", "sleep 30")
	if err := dummy.Start(); err != nil {
		t.Fatal(err)
	}
	dummyPID := dummy.Process.Pid
	t.Cleanup(func() {
		if dummy.Process != nil {
			_ = dummy.Process.Kill()
			_ = dummy.Wait()
		}
	})

	dummyInfo, err := readLinuxProcInfo(dummyPID)
	if err != nil {
		t.Fatalf("reading dummy proc info: %v", err)
	}

	// Start a real TERM-ignoring process that will keep the grace-period wait alive
	// and force escalation to Phase 2 (SIGKILL).
	termIgnore := exec.Command("/bin/sh", "-c", "trap '' TERM; sleep 30")
	if err := termIgnore.Start(); err != nil {
		t.Fatal(err)
	}
	termIgnorePID := termIgnore.Process.Pid
	t.Cleanup(func() {
		if termIgnore.Process != nil {
			_ = termIgnore.Process.Kill()
			_ = termIgnore.Wait()
		}
	})
	termIgnoreInfo, err := readLinuxProcInfo(termIgnorePID)
	if err != nil {
		t.Fatalf("reading termIgnore proc info: %v", err)
	}

	// Simulate a candidate list containing a recycled PID (stale start time) alongside
	// the active TERM-ignoring process.
	procs := []linuxProcInfo{
		{pid: dummyPID, startTime: dummyInfo.startTime - 1000}, // recycled/mismatched identity
		{pid: termIgnorePID, startTime: termIgnoreInfo.startTime},
	}

	// 1. Pre-signal validation must reject dummyPID on SIGTERM
	signaled := signalProcessIfIdentityMatches(dummyPID, dummyInfo.startTime-1000, syscall.SIGTERM)
	if signaled {
		t.Fatal("signalProcessIfIdentityMatches signaled dummyPID despite start time mismatch")
	}

	// 2. Waiting on identities must recognize dummyPID as dead/unmatched while waiting for termIgnore
	if waitForProcIdentitiesExit(procs, 100*time.Millisecond) {
		t.Fatal("waitForProcIdentitiesExit returned true while termIgnore was still running")
	}

	// 3. Pre-signal validation must reject dummyPID on SIGKILL escalation
	signaledKill := signalProcessIfIdentityMatches(dummyPID, dummyInfo.startTime-1000, syscall.SIGKILL)
	if signaledKill {
		t.Fatal("signalProcessIfIdentityMatches signaled dummyPID on SIGKILL escalation despite start time mismatch")
	}

	// Verify dummy is still running and unharmed
	if !pidAlive(dummyPID) {
		t.Fatalf("unrelated process %d was killed during grace period escalation", dummyPID)
	}
}

func TestLinuxPTYCloseReapsLateChildSpawnedInTERMHandler(t *testing.T) {
	// A parent process traps TERM: on receiving TERM, it spawns a background sleep job
	// in the same session and exits immediately (0.05s).
	conn, leaderPID, leaderStartTime := startLinuxSession(t, "/bin/bash", `trap 'sleep 30 & exit 0' TERM; printf 'trap-ready\n'; while true; do sleep 1; done`)

	waitForPTYOutput(t, conn, "trap-ready", "waiting for trap readiness")

	// Close() will send SIGTERM. The parent's trap handler will spawn a late child
	// in the session and exit. Close() must discover and reap this late child.
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	requireLinuxSessionGone(t, leaderPID, leaderStartTime, "late child spawned during TERM handler in session %d survived Close()")
}

// startLinuxSession launches script under shell and returns the connection
// plus the session leader's pid and start time.
func startLinuxSession(t *testing.T, shell, script string) (ptyConn, int, uint64) {
	t.Helper()
	conn, err := newConPTY(t.TempDir(), shell, []string{"-c", script}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatal(err)
	}
	linuxConn, ok := conn.(*linuxPTYConn)
	if !ok {
		t.Fatalf("connection type = %T", conn)
	}
	return conn, linuxConn.leaderPID, linuxConn.leaderStartTime
}

// waitForPTYOutput reads once from conn and requires marker in the output.
func waitForPTYOutput(t *testing.T, conn ptyConn, marker, what string) {
	t.Helper()
	ready := make([]byte, 128)
	n, err := conn.Read(ready)
	if err != nil || !strings.Contains(string(ready[:n]), marker) {
		t.Fatalf("%s: output=%q err=%v", what, ready[:n], err)
	}
}

// requireLinuxSessionGone waits up to 2s for the session to die; failFmt takes the leader pid.
func requireLinuxSessionGone(t *testing.T, pid int, startTime uint64, failFmt string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for linuxSessionAlive(pid, startTime) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if linuxSessionAlive(pid, startTime) {
		t.Fatalf(failFmt, pid)
	}
}
