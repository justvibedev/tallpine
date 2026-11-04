//go:build darwin || linux

package sessionreap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// startDetached runs a shell tagged with id that backgrounds a sleep in its
// own session and exits, so the sleep is reparented away from the test, as an
// agent's `setsid server &` would be. The sleeper is this test binary (see
// helperSleepEnv). It returns the sleeper's pid.
func startDetached(t *testing.T, id string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `nohup "$0" >/dev/null 2>&1 & echo $!`, os.Args[0])
	cmd.Env = append(os.Environ(), MarkerEnv+"="+id, helperSleepEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(out.String()))
	if err != nil {
		t.Fatalf("pid %q: %v", out.String(), err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	return pid
}

func TestReapStopsDetachedProcessAndSparesOtherSession(t *testing.T) {
	stamp := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	mine := startDetached(t, "reaptest-a-"+stamp)
	other := startDetached(t, "reaptest-b-"+stamp)

	r := New()
	n, err := r.Reap(context.Background(), Match{SessionID: "reaptest-a-" + stamp, StartedBefore: time.Now().Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("Reap signalled %d processes, want the detached sleep", n)
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(mine) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(mine) {
		t.Fatalf("detached process %d survived the reap", mine)
	}
	if !alive(other) {
		t.Fatalf("other session's process %d was stopped", other)
	}
}
