//go:build e2e && !windows

package cli_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestE2E_QuitNamesKilledBackgroundJobs drives the real daemon and real child
// processes through the two ways a restart treats a worker's background jobs:
// a bare stop leaves the host and its jobs running and must claim nothing, and
// a stop that parks agents (what the app's quit, and so an install, sends) must
// really end the jobs and tell each worker which ones.
func TestE2E_QuitNamesKilledBackgroundJobs(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a daemon and agent hosts")
	}
	e := newEnv(t)
	rig := newFakeACPRig(t, e)
	defer killLeftovers(e.dataDir) // chat-host helpers outlive the daemon

	var log bytes.Buffer
	start := func() chan struct{} {
		t.Helper()
		cmd := exec.Command(tallpineBin, "daemon")
		cmd.Env = rig.environ
		cmd.Stdout, cmd.Stderr = &log, &log
		if err := cmd.Start(); err != nil {
			t.Fatalf("spawn daemon: %v", err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			select {
			case <-done:
			default:
				_ = cmd.Process.Kill()
				<-done
			}
		})
		waitUntil(t, 45*time.Second, "daemon ready", func() bool {
			out, _ := e.runEnv(t, rig.environ, "status", "--json")
			return strings.Contains(out, `"state": "ready"`)
		})
		return done
	}
	stop := func(done chan struct{}, args ...string) {
		t.Helper()
		rig.tallpine(nil, append([]string{"stop"}, args...)...)
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			t.Fatalf("daemon did not exit\n%s", tail(log.String(), 4000))
		}
	}
	spawn := func(name, prompt string) string {
		t.Helper()
		out := rig.tallpine(nil, "spawn", "--project", "repo", "--kind", "worker", "--mode", "chat", "--harness", "claude-code",
			"--skip-agent-check", "--name", name, "--prompt", prompt)
		for _, line := range strings.Split(out, "\n") { // a warning can precede it
			if f := strings.Fields(line); len(f) >= 3 && f[0] == "spawned" {
				return f[2]
			}
		}
		t.Fatalf("spawn %s output %q", name, out)
		return ""
	}
	jobPID := func(name string) int {
		t.Helper()
		var pid int
		waitUntil(t, 30*time.Second, "pid of job "+name, func() bool {
			raw, err := os.ReadFile(filepath.Join(rig.ctl, "pid-"+name))
			if err != nil {
				return false
			}
			pid, err = strconv.Atoi(string(raw))
			return err == nil
		})
		return pid
	}
	tracked := func(id, name string) {
		t.Helper()
		waitUntil(t, 30*time.Second, "tracker to list job "+name, func() bool {
			return strings.Contains(rig.tallpine(nil, "session", "get", id, "--json"), "job "+name)
		})
	}
	messagesTo := func(id, substr string) int {
		raw, _ := os.ReadFile(filepath.Join(rig.ctl, "prompts.log"))
		n := 0
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "/"+id) && strings.Contains(line, substr) {
				n++
			}
		}
		return n
	}

	done := start()
	rig.tallpine(nil, "project", "add", "--path", rig.repo, "--id", "repo")

	// A bare stop: the chat host survives, so its job does too, and nothing may
	// tell the worker that job was killed.
	survivor := spawn("survivor", "BGJOB-survivor start")
	survivorPID := jobPID("survivor")
	tracked(survivor, "survivor")
	stop(done)
	if !processRunning(survivorPID) {
		t.Fatalf("bare stop killed the job (pid %d)", survivorPID)
	}
	done = start()
	time.Sleep(3 * time.Second) // the boot passes run in the background
	if n := messagesTo(survivor, "Killed by the restart"); n != 0 {
		t.Fatalf("surviving job reported as killed %d times\n%s", n, tail(log.String(), 4000))
	}
	if !processRunning(survivorPID) {
		t.Fatalf("job died across the bare restart (pid %d)", survivorPID)
	}

	// A parking stop, as an install sends: one worker idle, one mid-turn.
	idle := spawn("idle", "BGJOB-idlejob start")
	busy := spawn("busy", "BGJOB-busyjob HOLD-busy start")
	idlePID, busyPID := jobPID("idlejob"), jobPID("busyjob")
	tracked(idle, "idlejob")
	tracked(busy, "busyjob")
	stop(done, "--stop-agents")
	for name, pid := range map[string]int{"idlejob": idlePID, "busyjob": busyPID, "survivor": survivorPID} {
		waitUntil(t, 20*time.Second, "job "+name+" to be stopped by the quit", func() bool { return !processRunning(pid) })
	}

	done = start()
	defer stop(done)
	waitUntil(t, 60*time.Second, "both workers to be told which jobs died", func() bool {
		return messagesTo(idle, `"job idlejob" (local_bash)`) >= 1 && messagesTo(busy, `"job busyjob" (local_bash)`) >= 1
	})
	time.Sleep(2 * time.Second) // let a duplicate land if there is one
	for _, c := range []struct{ id, want string }{
		{idle, `"job idlejob" (local_bash)`},
		{busy, `"job busyjob" (local_bash)`},
	} {
		if n := messagesTo(c.id, c.want); n != 1 {
			t.Errorf("session %s told about %s %d times, want once", c.id, c.want, n)
		}
	}
	if n := messagesTo(idle, "busyjob"); n != 0 {
		t.Errorf("idle worker told about another worker's job %d times", n)
	}
	// B617: the survivor's job outlived one restart and the parking stop killed
	// it, so its idle agent is told, naming the job.
	waitUntil(t, 60*time.Second, "the survivor to be told its job died", func() bool {
		return messagesTo(survivor, `"job survivor" (local_bash)`) >= 1
	})
	time.Sleep(2 * time.Second)
	if n := messagesTo(survivor, `"job survivor" (local_bash)`); n != 1 {
		t.Errorf("survivor told about its job %d times, want once", n)
	}
}

func waitUntil(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// processRunning reports whether pid is a live, non-zombie process.
func processRunning(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	state := strings.TrimSpace(string(out))
	return err == nil && state != "" && !strings.HasPrefix(state, "Z")
}
