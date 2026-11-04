package daemon

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonmeta"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// A second start that is refused because a daemon is already serving must not
// rotate daemon.token: the live daemon still holds the old token in memory.
func TestRefusedSecondStartLeavesDaemonTokenUnchanged(t *testing.T) {
	// Run chdirs into the data dir; t.Chdir restores the original cwd on cleanup.
	t.Chdir(t.TempDir())
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("TALLPINE_DATA_DIR", filepath.Join(dir, "data"))
	runPath := filepath.Join(dir, "running.json")
	t.Setenv("TALLPINE_RUN_FILE", runPath)
	t.Setenv("TALLPINE_DAEMON_AUTH", "")

	pid := os.Getpid()
	srv := httptest.NewServer(healthzBody(daemonmeta.ServiceName, pid))
	t.Cleanup(srv.Close)
	_, portStr, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	port, _ := strconv.Atoi(portStr)
	if err := runfile.Write(runPath, runfile.Info{PID: pid, Port: port, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	tokenPath := daemontoken.Path(runPath)
	before, err := daemontoken.Ensure(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	err = Run()
	if err == nil || !strings.Contains(err.Error(), "refusing to start") {
		t.Fatalf("Run = %v, want the already-running refusal", err)
	}
	after, err := daemontoken.Read(tokenPath)
	if err != nil || after != before {
		t.Fatalf("daemon.token changed on a refused start (err=%v)", err)
	}
}
