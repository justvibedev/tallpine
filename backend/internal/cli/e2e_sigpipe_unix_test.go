//go:build e2e && !windows

package cli_test

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestE2E_DaemonClosedOutputPipeShutdownRemovesRunFile(t *testing.T) {
	e := newEnv(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	go func() {
		_, _ = io.Copy(io.Discard, r)
	}()

	cmd := exec.Command(tallpineBin, "daemon")
	cmd.Env = e.environ("")
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn tallpine daemon: %v", err)
	}
	_ = w.Close()

	processDone := make(chan struct{})
	var waitErr error
	go func() {
		waitErr = cmd.Wait()
		close(processDone)
	}()
	t.Cleanup(func() {
		select {
		case <-processDone:
			return
		default:
		}
		_ = cmd.Process.Kill()
		select {
		case <-processDone:
		case <-time.After(5 * time.Second):
			t.Error("daemon process did not exit after kill")
		}
	})

	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := os.Stat(e.runFile)
		if err == nil && info.Size() > 0 {
			break
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("stat run-file: %v", err)
		}
		select {
		case <-processDone:
			t.Fatalf("daemon exited before writing run-file: %v", waitErr)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not write run-file within 10s")
		}
		time.Sleep(50 * time.Millisecond)
	}

	_ = r.Close()

	client := e.client(2 * time.Second)
	resp, err := client.Get(e.url("/healthz"))
	if err != nil {
		t.Fatalf("GET /healthz after output reader closed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", resp.StatusCode)
	}

	shutdownReq, err := http.NewRequest(http.MethodPost, e.url("/shutdown"), nil)
	if err != nil {
		t.Fatalf("build shutdown request: %v", err)
	}
	e.authorize(t, shutdownReq)
	resp, err = client.Do(shutdownReq)
	if err != nil {
		t.Fatalf("POST /shutdown after output reader closed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /shutdown = %d, want 202", resp.StatusCode)
	}

	select {
	case <-processDone:
		if waitErr != nil {
			t.Fatalf("daemon exited via signal/error after output pipe closed: %v", waitErr)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("daemon did not stop after shutdown request")
	}

	if _, err := os.Stat(e.runFile); !os.IsNotExist(err) {
		t.Fatalf("run-file after closed-pipe shutdown = %v, want missing", err)
	}
}
