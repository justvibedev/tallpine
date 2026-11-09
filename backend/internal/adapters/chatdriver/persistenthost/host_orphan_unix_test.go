//go:build darwin || linux

package persistenthost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/sessionreap"
)

// deafAgent ignores SIGTERM and never reads stdin, so neither a closed stdin
// nor a polite signal ends it, the hung agent B607 describes.
var deafAgent = []string{"/bin/sh", "-c", `trap "" TERM; sleep 300 & wait; sleep 300`}

func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// killHost starts a real detached host over the deaf agent and SIGKILLs the
// host, leaving the orphan. It returns the descriptor the host published.
func killHost(t *testing.T, id, workdir string) Descriptor {
	t.Helper()
	return killHostOf(t, id, workdir, deafAgent)
}

func killHostOf(t *testing.T, id, workdir string, argv []string) Descriptor {
	t.Helper()
	// t.TempDir paths are too long for the host's unix socket on macOS.
	dataDir, err := os.MkdirTemp("", "pho")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	t.Setenv("PHO_DATA", dataDir)
	cfg := Config{
		SessionID: id, DataDir: dataDir, Workdir: workdir,
		Env:  append(os.Environ(), sessionreap.MarkerEnv+"="+id),
		Argv: argv,
	}
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Stdin.Close() })
	d, err := readDescriptor(dataDir, id)
	if err != nil {
		t.Fatal(err)
	}
	if d.Provider == nil || d.Provider.Start.IsZero() || d.HostStart.IsZero() || d.Provider.Marker != id {
		t.Fatalf("descriptor lacks the launch record: %+v provider=%+v", d, d.Provider)
	}
	t.Cleanup(func() { _ = syscall.Kill(-d.Provider.PID, syscall.SIGKILL) })
	if err := syscall.Kill(d.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "host to die", 5*time.Second, func() bool { return !pidAlive(d.PID) })
	if !pidAlive(d.Provider.PID) {
		t.Fatal("provider died with the host; the orphan scenario was not reproduced")
	}
	return d
}

// innocent is an unrelated process the reap must never touch. It is this
// test's child, so a killed one stays a zombie that kill(0) still reports as
// alive; exited is what tells the difference.
type innocent struct {
	pid    int
	exited chan struct{}
}

func (i innocent) running() bool {
	select {
	case <-i.exited:
		return false
	default:
		return true
	}
}

func startInnocent(t *testing.T) innocent {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	i := innocent{pid: cmd.Process.Pid, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(i.exited) }()
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-i.exited })
	return i
}

func rewriteDescriptor(t *testing.T, dataDir string, d Descriptor) {
	t.Helper()
	if err := writeDescriptor(dataDir, d); err != nil {
		t.Fatal(err)
	}
}

func dataDirOf(t *testing.T) string { t.Helper(); return os.Getenv("PHO_DATA") }

func TestReconcileReapsHungAgentOfSIGKILLedHost(t *testing.T) {
	d := killHost(t, "orphan-kept", t.TempDir())
	dataDir := dataDirOf(t)
	// The session is live, so its host is in the keep set: that must not spare
	// an agent whose host is proven dead.
	if err := Reconcile(context.Background(), dataDir, map[string]struct{}{"orphan-kept": {}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orphaned agent to be reaped", 8*time.Second, func() bool { return !pidAlive(d.Provider.PID) })
	if path, _ := descriptorPath(dataDir, d.SessionID); fileExists(path) {
		t.Fatal("descriptor survived the reap")
	}
	if path, _ := lockPath(dataDir, d.SessionID); fileExists(path) {
		t.Fatal("launch lock survived the reap")
	}
}

func TestReconcileReapsChildOfProviderThatExitsOnTERM(t *testing.T) {
	workdir := t.TempDir()
	pidFile := filepath.Join(workdir, "child.pid")
	// The provider dies on SIGTERM; the child it leaves in its group ignores it.
	script := `/bin/sh -c 'trap "" TERM; while :; do sleep 1; done' & echo $! >child.tmp; mv child.tmp child.pid; wait`
	d := killHostOf(t, "orphan-term", workdir, []string{"/bin/sh", "-c", script})
	child := 0
	waitFor(t, "child pid", 5*time.Second, func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		child, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil
	})
	if !pidAlive(child) {
		t.Fatal("child died with the host; the scenario was not reproduced")
	}
	if err := Reconcile(context.Background(), dataDirOf(t), nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "provider to exit", 5*time.Second, func() bool { return !pidAlive(d.Provider.PID) })
	waitFor(t, "TERM-ignoring child to be killed", 8*time.Second, func() bool { return !pidAlive(child) })
}

func TestRespawnReapsPredecessorsHungAgent(t *testing.T) {
	workdir := t.TempDir()
	d := killHost(t, "orphan-respawn", workdir)
	dataDir := dataDirOf(t)
	cfg := Config{
		SessionID: d.SessionID, DataDir: dataDir, Workdir: workdir,
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, cfg.SessionID) })
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Stdin.Close() }()
	waitFor(t, "old agent to be reaped", 8*time.Second, func() bool { return !pidAlive(d.Provider.PID) })
}

func TestReconcileLeavesReusedProviderPIDAlone(t *testing.T) {
	d := killHost(t, "orphan-reused", t.TempDir())
	dataDir := dataDirOf(t)
	other := startInnocent(t)
	// The recorded provider pid now belongs to an unrelated process.
	d.Provider = &ProviderRecord{PID: other.pid, Start: time.Now().Add(-48 * time.Hour), Marker: d.SessionID}
	rewriteDescriptor(t, dataDir, d)
	if err := Reconcile(context.Background(), dataDir, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if !other.running() {
		t.Fatal("reconcile killed an unrelated process that reused the provider pid")
	}
}

func TestReconcileSeesThroughReusedHostPID(t *testing.T) {
	d := killHost(t, "orphan-host-reused", t.TempDir())
	dataDir := dataDirOf(t)
	other := startInnocent(t)
	// The dead host's pid now belongs to an unrelated process, which still
	// looks alive to a bare kill(0) probe and holds the lock's recorded pid.
	d.PID = other.pid
	d.HostStart = d.HostStart.Add(-time.Hour)
	rewriteDescriptor(t, dataDir, d)
	path, _ := lockPath(dataDir, d.SessionID)
	if err := os.WriteFile(path, []byte(itoa(d.PID)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), dataDir, map[string]struct{}{d.SessionID: {}}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "orphaned agent to be reaped", 8*time.Second, func() bool { return !pidAlive(d.Provider.PID) })
	if !other.running() {
		t.Fatal("reconcile killed the unrelated process that reused the host pid")
	}
}

func TestReconcileSparesLiveHostAfterClockStep(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "pho")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	cfg := Config{
		SessionID: "clock-step", DataDir: dataDir, Workdir: t.TempDir(),
		Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
		Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
	}
	t.Cleanup(func() { _ = Shutdown(context.Background(), dataDir, cfg.SessionID) })
	transport, err := ConnectOrStart(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Stdin.Close() }()
	d, err := readDescriptor(dataDir, cfg.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// A recorded start that no longer matches, but the host still answers.
	d.HostStart = d.HostStart.Add(-time.Hour)
	rewriteDescriptor(t, dataDir, d)
	if err := Reconcile(context.Background(), dataDir, map[string]struct{}{cfg.SessionID: {}}); err != nil {
		t.Fatal(err)
	}
	if !pidAlive(d.Provider.PID) || !pidAlive(d.PID) {
		t.Fatal("a live host was treated as dead after its start time stopped matching")
	}
}

// A mismatched start time plus a probe that says nothing must leave a live
// host and its agent alone: only a refused connection proves the host dead.
func TestReconcileSparesLiveHostWhenProbeIsInconclusive(t *testing.T) {
	notSocket := filepath.Join(t.TempDir(), "plain-file")
	if err := os.WriteFile(notSocket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name    string
		ctx     context.Context
		address func(Descriptor) string
	}{
		{"cancelled context", cancelled, func(d Descriptor) string { return d.Address }},
		{"address is not a socket", context.Background(), func(Descriptor) string { return "unix:" + notSocket }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir, err := os.MkdirTemp("", "pho")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
			cfg := Config{
				SessionID: "inconclusive", DataDir: dataDir, Workdir: t.TempDir(),
				Env:  append(os.Environ(), "TALLPINE_CHAT_HOST_PROVIDER_HELPER=1"),
				Argv: []string{os.Args[0], "-test.run=TestProviderHelper"},
			}
			transport, err := ConnectOrStart(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = transport.Stdin.Close() }()
			live, err := readDescriptor(dataDir, cfg.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				rewriteDescriptor(t, dataDir, live)
				_ = Shutdown(context.Background(), dataDir, cfg.SessionID)
			})
			forged := live
			forged.HostStart = live.HostStart.Add(-time.Hour)
			forged.Address = tt.address(live)
			rewriteDescriptor(t, dataDir, forged)
			if err := Reconcile(tt.ctx, dataDir, map[string]struct{}{cfg.SessionID: {}}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(200 * time.Millisecond)
			if !pidAlive(live.Provider.PID) || !pidAlive(live.PID) {
				t.Fatal("an inconclusive probe got a live host's agent stopped")
			}
			if path, _ := descriptorPath(dataDir, cfg.SessionID); !fileExists(path) {
				t.Fatal("an inconclusive probe cleared a live host's descriptor")
			}
			if path, _ := lockPath(dataDir, cfg.SessionID); !fileExists(path) {
				t.Fatal("an inconclusive probe released a live host's launch lock")
			}
		})
	}
}

func TestReconcileKeepsRecordWhenOwnershipIsUnknown(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("needs the lsof-based working directory lookup to fail")
	}
	d := killHost(t, "orphan-unknown", t.TempDir())
	dataDir := dataDirOf(t)
	path := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir()) // lsof cannot be found.
	if err := Reconcile(context.Background(), dataDir, nil); err != nil {
		t.Fatal(err)
	}
	if !pidAlive(d.Provider.PID) {
		t.Fatal("agent was stopped although its working directory was unreadable")
	}
	descriptor, _ := descriptorPath(dataDir, d.SessionID)
	if !fileExists(descriptor) {
		t.Fatal("the only record of a spared agent was cleared")
	}
	t.Setenv("PATH", path)
	if err := Reconcile(context.Background(), dataDir, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "retry to reap the agent", 8*time.Second, func() bool { return !pidAlive(d.Provider.PID) })
}

func TestReconcileNeverReapsUnderLocalCI(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ci := filepath.Join(root, "local-ci")
	if err := os.MkdirAll(ci, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TALLPINE_LOCAL_CI_DIR", ci)
	d := killHost(t, "orphan-ci", ci)
	if err := Reconcile(context.Background(), dataDirOf(t), nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second) // Longer than the reaper's grace.
	if !pidAlive(d.Provider.PID) {
		t.Fatal("reconcile killed a process running under the local CI directory")
	}
}

func TestDescriptorWithoutLaunchRecordIsOnlyCleared(t *testing.T) {
	dataDir, err := os.MkdirTemp("", "pho")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dataDir) })
	// A descriptor from a host predating the record has nothing to prove with.
	legacy := map[string]any{"version": ProtocolVersion, "sessionId": "legacy", "address": "unix:/nope", "token": "t", "pid": deadPID(t), "startedAt": time.Now()}
	raw, _ := json.Marshal(legacy)
	path, _ := descriptorPath(dataDir, "legacy")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reconcile(context.Background(), dataDir, nil); err != nil {
		t.Fatal(err)
	}
	if fileExists(path) {
		t.Fatal("dead legacy descriptor was not cleared")
	}
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
