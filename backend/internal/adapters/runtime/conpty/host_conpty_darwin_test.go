//go:build darwin

package conpty

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestDarwinPTYConnStreamsResizesAndReportsExit(t *testing.T) {
	runPTYConnStreamsResizesAndReportsExit(t, "Darwin", func(c ptyConn) (*os.File, bool) {
		dc, ok := c.(*darwinPTYConn)
		if !ok {
			return nil, false
		}
		return dc.pty, true
	})
}

func TestDarwinDefaultSpawnHostEndToEnd(t *testing.T) { runDefaultSpawnHostEndToEnd(t) }

func TestDarwinPTYCloseReapsTermIgnoringProcessGroup(t *testing.T) {
	conn, err := newConPTY(t.TempDir(), "/bin/sh", []string{
		"-c", `trap '' TERM; (trap '' TERM; printf 'child-ready\n'; sleep 30) & wait`,
	}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatal(err)
	}
	pgid := conn.PID()
	if !darwinProcessGroupAlive(pgid) {
		t.Fatalf("process group %d was not alive after launch", pgid)
	}
	ready := make([]byte, 128)
	n, err := conn.Read(ready)
	if err != nil || !strings.Contains(string(ready[:n]), "child-ready") {
		t.Fatalf("waiting for process-group fixture readiness: output=%q err=%v", ready[:n], err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for darwinProcessGroupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if darwinProcessGroupAlive(pgid) {
		t.Fatalf("process group %d survived PTY close", pgid)
	}
}

// An interactive shell ignores SIGTERM. Closing must not wait out the full
// grace for it: Tallpine closes a terminal the way a closing terminal window would.
func TestDarwinPTYCloseDoesNotWaitOutAShellThatIgnoresSIGTERM(t *testing.T) {
	conn, err := newConPTY(t.TempDir(), "/bin/sh", []string{"-c", `trap '' TERM; printf 'ready\n'; while :; do sleep 1; done`}, initialConPTYColumns, initialConPTYRows)
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	go func() {
		buf := make([]byte, 64)
		for {
			n, err := conn.Read(buf)
			if n > 0 && strings.Contains(string(buf[:n]), "ready") {
				close(ready)
				return
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("shell did not start")
	}
	started := time.Now()
	if err := conn.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= darwinPTYCloseGrace {
		t.Fatalf("Close took %s; a SIGTERM-ignoring shell waited out the %s grace", elapsed, darwinPTYCloseGrace)
	}
}
