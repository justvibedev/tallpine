//go:build e2e

package cli_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestE2E_DaemonRestartResumesChat drives the real `tallpine daemon` binary with a
// scripted ACP agent (testdata/fakeacp, injected through TALLPINE_CLAUDE_ACP_COMMAND)
// and restarts it with Chat work in flight: an orchestrator mid-turn, a worker
// mid-turn, a worker with a queued `tallpine send` behind a busy turn, and two
// pending worker reports (one needs_input). It pins:
//
//  1. graceful shutdown completes promptly, with no forced close;
//  2. every Chat session resumes by itself, with no resume-agent call;
//  3. each pending report reaches the orchestrator exactly once;
//  4. the queued `tallpine send` reaches its worker exactly once;
//  5. a worker turn that fails after the restart yields exactly one
//     "[auto] turn_ended" notice (#140/#142).
func TestE2E_DaemonRestartResumesChat(t *testing.T) {
	t.Parallel()
	runDaemonRestartScenario(t, false)
}

// TestE2E_DaemonRestartDeliveryWithManualResume runs the same scenario but
// resumes the exited sessions with `session resume-agent`, isolating (1) and
// (3)-(5) from the automatic-resume requirement (2).
func TestE2E_DaemonRestartDeliveryWithManualResume(t *testing.T) {
	t.Parallel()
	runDaemonRestartScenario(t, true)
}

// TestE2E_DaemonRestartStalledResume: after an app quit the first session/load
// stalls past the handshake timeout, then succeeds. A send issued during the
// stall must be delivered exactly once, with no manual resume-agent.
func TestE2E_DaemonRestartStalledResume(t *testing.T) {
	t.Parallel()
	runDaemonRestartScenario(t, false, true)
}

func runDaemonRestartScenario(t *testing.T, manualResume bool, stall ...bool) {
	if testing.Short() {
		t.Skip("spawns a daemon and agent hosts")
	}
	e := newEnv(t)
	root := t.TempDir()
	ctl := filepath.Join(root, "ctl")
	bin := filepath.Join(root, "bin")
	home := filepath.Join(root, "home")
	for _, d := range []string{ctl, bin, home} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fakeACP := filepath.Join(bin, "fakeacp")
	if out, err := exec.Command("go", "build", "-o", fakeACP, "./testdata/fakeacp").CombinedOutput(); err != nil {
		t.Fatalf("build fakeacp: %v\n%s", err, out)
	}
	// A stand-in `claude` so binary resolution succeeds; its probes just fail,
	// which the chat launch treats as inconclusive.
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	environ := append(e.environ(""),
		"HOME="+home,
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"TALLPINE_CLAUDE_ACP_COMMAND="+fakeACP,
		"TALLPINE_FAKEACP_DIR="+ctl,
		"TALLPINE_TURN_FALLBACK_DELAY=2s", "TALLPINE_QUIET_REPORT_DELAY=2s", "TALLPINE_ACP_HANDSHAKE_TIMEOUT=3s", // honoured only by the -tags e2e binary
	)
	tallpine := func(extra []string, args ...string) string {
		t.Helper()
		out, code := e.runEnv(t, append(append([]string{}, environ...), extra...), args...)
		if code != 0 {
			t.Fatalf("tallpine %s: exit %d\n%s", strings.Join(args, " "), code, out)
		}
		return out
	}

	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "t@example.com"}, {"config", "user.name", "t"},
		{"commit", "--allow-empty", "-m", "init"},
		{"remote", "add", "origin", bare}, {"push", "origin", "main"}, {"remote", "set-head", "origin", "main"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	var daemonLog bytes.Buffer
	startDaemon := func() (stop func() time.Duration) {
		cmd := exec.Command(tallpineBin, "daemon")
		cmd.Env = environ
		cmd.Stdout, cmd.Stderr = &daemonLog, &daemonLog
		if err := cmd.Start(); err != nil {
			t.Fatalf("spawn daemon: %v", err)
		}
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		t.Cleanup(func() {
			defer killLeftovers(e.dataDir) // chat-host helpers outlive the daemon
			select {
			case <-done:
			default:
				_ = cmd.Process.Kill()
				<-done
			}
		})
		// Generous: local CI runs this beside other suites on a loaded host.
		deadline := time.Now().Add(45 * time.Second)
		for {
			out, _ := e.runEnv(t, environ, "status", "--json")
			if strings.Contains(out, `"state": "ready"`) {
				break
			}
			if time.Now().After(deadline) {
				// SIGQUIT makes the Go runtime dump every goroutine to the
				// daemon log and exit, showing where a hung startup is blocked.
				// Wait for that exit (a loaded host can take far longer than a
				// few seconds to write the dump), then kill as a fallback; the
				// log is read only once the process is gone, so the copy into
				// daemonLog has finished and nothing races the read.
				_ = cmd.Process.Signal(syscall.SIGQUIT)
				dumped := true
				select {
				case <-done:
				case <-time.After(30 * time.Second):
					dumped = false
					_ = cmd.Process.Kill()
					<-done
				}
				t.Fatalf("daemon not ready; status:\n%s\ngoroutine dump written: %v\nlast daemon log lines:\n%s",
					out, dumped, tail(daemonLog.String(), 12000))
			}
			time.Sleep(100 * time.Millisecond)
		}
		return func() time.Duration {
			start := time.Now()
			tallpine(nil, "stop")
			select {
			case <-done:
			case <-time.After(15 * time.Second):
				t.Fatalf("daemon did not exit within 15s of stop")
			}
			return time.Since(start)
		}
	}

	prompts := func() []string {
		b, _ := os.ReadFile(filepath.Join(ctl, "prompts.log"))
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if !strings.Contains(l, "/background-tasks/") { // title/branch naming helpers
				out = append(out, l)
			}
		}
		return out
	}
	// count returns how many logged prompts contain every given substring.
	count := func(subs ...string) int {
		n := 0
	next:
		for _, l := range prompts() {
			for _, s := range subs {
				if !strings.Contains(l, s) {
					continue next
				}
			}
			n++
		}
		return n
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(40 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s\nprompts:\n%s\ndaemon log tail:\n%s", what, strings.Join(prompts(), "\n"), tail(daemonLog.String(), 6000))
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	// settle waits until no new prompt reached any agent for a quiet window, so a
	// duplicate delivery has had time to land without a fixed multi-second sleep.
	settle := func() {
		t.Helper()
		last, since := len(prompts()), time.Now()
		for time.Since(since) < time.Second {
			time.Sleep(100 * time.Millisecond)
			if n := len(prompts()); n != last {
				last, since = n, time.Now()
			}
		}
	}
	release := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ctl, "release-"+name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	spawn := func(name, kind, prompt string) string {
		t.Helper()
		out := tallpine(nil, "spawn", "--project", "repo", "--kind", kind, "--mode", "chat", "--harness", "claude-code",
			"--skip-agent-check", "--name", name, "--prompt", prompt)
		f := strings.Fields(out)
		if len(f) < 3 || f[0] != "spawned" {
			t.Fatalf("spawn %s output %q", name, out)
		}
		return f[2]
	}

	stopDaemon := startDaemon()
	tallpine(nil, "project", "add", "--path", repo, "--id", "repo")
	orch := spawn("orch", "orchestrator", "HOLD-orch start")
	w1 := spawn("w1", "worker", "HOLD-w1 start")
	w2 := spawn("w2", "worker", "HOLD-w2 start")
	waitFor("three held turns", func() bool {
		return count("HOLD-orch") == 1 && count("HOLD-w1") == 1 && count("HOLD-w2") == 1
	})
	tallpine(nil, "send", "--session", w2, "--message", "QUEUED-behind-w2")
	tallpine([]string{"TALLPINE_SESSION_ID=" + w1}, "report", "--needs-input", "--note", "REPORT-needs-w1")
	tallpine([]string{"TALLPINE_SESSION_ID=" + w2}, "report", "--done", "--note", "REPORT-done-w2")

	// An open plain SSE stream, like the desktop app's, must not hold shutdown
	// up until the force-close timeout (#141).
	req, err := http.NewRequest(http.MethodGet, e.url("/api/v1/events"), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.authorize(t, req)
	resp, err := e.client(0).Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("open event stream: resp=%v err=%v", resp, err)
	}
	streamEnded := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, resp.Body); close(streamEnded) }()

	elapsed := stopDaemon()
	select {
	case <-streamEnded:
	case <-time.After(2 * time.Second):
		t.Error("(1) event stream still open after the daemon exited")
	}
	if elapsed > 5*time.Second {
		t.Errorf("(1) graceful shutdown took %s", elapsed)
	}
	for _, bad := range []string{"forcing close", "graceful shutdown exceeded"} {
		if strings.Contains(daemonLog.String(), bad) {
			t.Errorf("(1) daemon log contains %q", bad)
		}
	}

	// A real app quit takes the detached chat hosts and their agents down with
	// the daemon, so the restart has to go through the resume path.
	killAgents := func() {
		for _, pattern := range []string{fakeACP, e.dataDir} {
			_ = exec.Command("pkill", "-9", "-f", pattern).Run()
		}
		time.Sleep(500 * time.Millisecond)
	}
	killAgents()

	stalling := len(stall) > 0 && stall[0]
	if stalling {
		// Armed before the daemon starts: it resumes the chats on its own at
		// startup, so arming after "ready" races that resume and the stall may
		// never be hit.
		if err := os.WriteFile(filepath.Join(ctl, "stall-load"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	stopDaemon = startDaemon()
	defer func() { stopDaemon() }()
	if stalling {
		waitFor("a session/load to stall", func() bool {
			_, err := os.Stat(filepath.Join(ctl, "stalled"))
			return err == nil
		})
		tallpine(nil, "send", "--session", w2, "--message", "SEND-during-stall")
		waitFor("the send during the stall", func() bool { return count("SEND-during-stall") >= 1 })
		settle()
		if _, err := os.Stat(filepath.Join(ctl, "stalled")); err != nil {
			t.Error("fake agent never stalled a session/load; the stall was not exercised")
		}
		if got := count("SEND-during-stall"); got != 1 {
			t.Errorf("send during stall delivered %d times by its agent, want 1", got)
		}
		return
	}
	if manualResume {
		// The daemon resumes these by itself too; a manual Resume that finds it
		// already done or in progress is the same outcome.
		for _, id := range []string{orch, w1, w2} {
			out, code := e.runEnv(t, environ, "session", "resume-agent", id)
			if code != 0 && !strings.Contains(out, "AGENT_RESUME_IN_PROGRESS") && !strings.Contains(out, "AGENT_NOT_EXITED") {
				t.Fatalf("tallpine session resume-agent %s: exit %d\n%s", id, code, out)
			}
		}
	}

	// (2) No resume-agent call anywhere: the sessions must come back alone and
	// take what was pending for them. Counts are what the agents received.
	waitFor("the orchestrator to take the pending reports", func() bool {
		return count("REPORT-needs-w1") >= 1 && count("REPORT-done-w2") >= 1
	})
	waitFor("the restart resume notes", func() bool { return count("came from the restart, not the user") >= 3 })

	// (5) A worker turn fails after the restart while a send is queued behind
	// it (#142): the failed turn must still yield exactly one notice.
	// The turn stays parked until the second send is queued behind it; releasing
	// it first would race the send, which then lands on an idle session.
	// w1's resume turn must have fully ended (its notice is the one w1 notice
	// asserted below) before the failing turn is provoked. Under load the resume
	// turn can still be projected as running: the send is then steered into it,
	// not started as its own turn, and that turn's later successful completion
	// drains the send queued behind it (B358).
	waitFor("w1's resume turn to end", func() bool { return count("[auto] turn_ended", "[w1]") >= 1 })
	w1Base := count("[auto] turn_ended", "[w1]") // the resume turn's notice; counts below are relative to it
	tallpine(nil, "send", "--session", w1, "--message", "HOLD-fail provoke")
	waitFor("w1's failing turn to start", func() bool { return count("HOLD-fail provoke") == 1 })
	out := tallpine(nil, "send", "--session", w1, "--message", "QUEUED-behind-fail")
	if !strings.Contains(out, "queued for next turn") {
		t.Fatalf("(5) send during the parked turn was not queued behind it:\n%s", out)
	}
	release("fail", "fail")
	// A failed primary turn is announced like a completed one: afterProject
	// (service/chat/controller_after_project.go) reports the same chat.turn.completed
	// activity signal for it, which turnnotify turns into a turn_ended notice, and
	// applyTurnCompleted records the durable turn fallback for state Failed. It is
	// therefore always exactly one w1 notice, due ChatTurnFallbackDelay (2s here)
	// after the failure and delivered once the orchestrator is free. Wait for that
	// notice instead of a fixed settle: under load it lands after the 3s sleep,
	// even after the second restart (B359).
	waitFor("the failed turn's notice", func() bool { return count("[auto] turn_ended", "[w1]") >= w1Base+1 })

	// assertOnce checks the exactly-once counts. A failed turn holds the queue,
	// so the send behind it (5) is not delivered until the next resume: the
	// held send's delivery ends a turn of its own after the second restart.
	assertOnce := func(stage string, afterSecondRestart bool) {
		t.Helper()
		// The failed turn's notice, plus the held send's own turn after the second restart.
		heldDelivered, w1Notices := 0, w1Base+1
		if afterSecondRestart {
			heldDelivered, w1Notices = 1, w1Base+2
		}
		// w2's delivered queued send ends a turn of its own, so w2 owes the
		// orchestrator one turn_ended notice too; each must arrive exactly once.
		for _, c := range []struct {
			what string
			subs []string
			want int
		}{
			// w2 now ends two turns after the restart: the resume note and the queued send.
			// B218: the three turns in flight at the (installer-style) stop each get
			// exactly one restart note; the idle probes below got none.
			{"(6) restart resume note", []string{"came from the restart, not the user"}, 3},
			{"(3) needs_input report", []string{"REPORT-needs-w1"}, 1},
			{"(3) done report", []string{"REPORT-done-w2"}, 1},
			{"(4) queued send", []string{"QUEUED-behind-w2"}, 1},
			{"(4) queued send's turn notice", []string{"[auto] turn_ended", "[w2]"}, 2},
			{"(5) failure notice", []string{"[auto] turn_ended", "[w1]"}, w1Notices},
			{"(5) send held behind the failed turn", []string{"QUEUED-behind-fail"}, heldDelivered},
		} {
			if got := count(c.subs...); got != c.want {
				t.Errorf("%s: %s received %d times by its agent, want %d\nprompts:\n%s", stage, c.what, got, c.want, strings.Join(prompts(), "\n"))
			}
		}
	}
	settle()
	assertOnce("after restart", false)

	// A second quit and restart must not replay anything: reports end
	// acknowledged and notices stay delivered.
	stopDaemon()
	killAgents()
	stopDaemon = startDaemon()
	// Wait on the condition (the held send delivered and its turn's notice
	// back), not a fixed sleep that load can outrun; then let duplicates land.
	waitFor("the held send's delivery and its turn notice after the second restart", func() bool {
		return count("QUEUED-behind-fail") >= 1 && count("[auto] turn_ended", "[w1]") >= w1Base+2
	})
	settle()
	assertOnce("after second restart", true)
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
