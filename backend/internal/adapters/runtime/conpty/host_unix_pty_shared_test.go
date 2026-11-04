//go:build darwin || linux

package conpty

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"

	"tallpine.eu/tallpine/backend/internal/testenv"
)

// TestMain lets the detached-spawn integration test re-exec this test binary
// through the same hidden pty-host entrypoint used by the production Tallpine binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		os.Exit(RunHost(os.Args[2:], os.Stdout))
	}
	testenv.Main(m)
}

func runPTYConnStreamsResizesAndReportsExit(t *testing.T, platform string, ptyOf func(ptyConn) (*os.File, bool)) {
	t.Helper()
	conn, err := newConPTY(t.TempDir(), "/bin/sh", []string{
		"-c", `printf 'ready\n'; IFS= read -r line; printf 'received:%s\n' "$line"; exit 7`,
	}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	ptyFile, ok := ptyOf(conn)
	if !ok {
		t.Fatalf("connection type = %T", conn)
	}
	if err := conn.Resize(101, 43); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	size, err := pty.GetsizeFull(ptyFile)
	if err != nil {
		t.Fatalf("GetsizeFull: %v", err)
	}
	if size.Cols != 101 || size.Rows != 43 {
		t.Fatalf("PTY size = %dx%d, want 101x43", size.Cols, size.Rows)
	}
	if err := conn.Resize(70_000, 43); err == nil {
		t.Fatal("Resize accepted a column count that overflows the " + platform + " winsize")
	}

	reader := bufio.NewReader(conn)
	ready, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("waiting for PTY readiness: %v", err)
	}
	if normalized := strings.ReplaceAll(ready, "\r", ""); normalized != "ready\n" {
		t.Fatalf("PTY readiness output = %q", normalized)
	}

	outputC := make(chan []byte, 1)
	go func() {
		var output bytes.Buffer
		_, _ = io.Copy(&output, reader)
		outputC <- output.Bytes()
	}()
	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	select {
	case <-conn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("PTY child did not exit")
	}
	code, exited := conn.ExitCode()
	if !exited || code != 7 {
		t.Fatalf("ExitCode = (%d, %v), want (7, true)", code, exited)
	}

	select {
	case output := <-outputC:
		text := strings.ReplaceAll(ready+string(output), "\r", "")
		if !strings.Contains(text, "ready\n") || !strings.Contains(text, "received:hello\n") {
			t.Fatalf("PTY output = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PTY output reader did not finish")
	}
}

func runDefaultSpawnHostEndToEnd(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	addr, hostPID, err := defaultSpawnHost(ctx, "spawn-e2e", t.TempDir(), []string{
		"env", "TALLPINE_PREFIX_VALUE=prefix", "/bin/sh", "-c",
		`printf '\033[c'; sleep 0.05; printf 'ready:%s:%s\n' "$TALLPINE_DIRECT_PTY_TEST" "$TALLPINE_PREFIX_VALUE"; IFS= read -r line; printf 'received:%s\n' "$line"; sleep 30`,
	}, map[string]string{"TALLPINE_DIRECT_PTY_TEST": "works"}, HostOptions{})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	// The request context owns startup only. A host that reported READY must
	// stay alive after that request ends so daemon restarts cannot kill agents.
	cancel()
	t.Cleanup(func() {
		_ = clientKill(addr)
		if pidAlive(hostPID) {
			if process, findErr := os.FindProcess(hostPID); findErr == nil {
				_ = process.Kill()
			}
		}
	})

	if err := clientSendInput(addr, "hello\n"); err != nil {
		t.Fatalf("send input: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		output, outputErr := clientGetOutput(context.Background(), addr, 20)
		if outputErr != nil {
			t.Fatalf("get output: %v", outputErr)
		}
		text := strings.ReplaceAll(output, "\r", "")
		if strings.Contains(text, "ready:works:prefix") && strings.Contains(text, "received:hello") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for host output: %q", text)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := clientKill(addr); err != nil {
		t.Fatalf("kill host: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for pidAlive(hostPID) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if pidAlive(hostPID) {
		t.Fatalf("detached pty-host pid %d survived kill", hostPID)
	}
}
