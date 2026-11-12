package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/daemonmeta"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// refusingDaemon answers its probes as the daemon at this test's pid and
// refuses /shutdown the way an enforcing peer check refuses this process.
func refusingDaemon(t *testing.T, cfg testConfig) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			_, _ = fmt.Fprintf(w, `{"status":"ok","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/readyz":
			_, _ = fmt.Fprintf(w, `{"status":"ready","service":%q,"pid":%d}`, daemonmeta.ServiceName, os.Getpid())
		case "/shutdown":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"message":"refused","code":"PEER_NOT_ADMITTED"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: os.Getpid(), Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestStopFallsBackToSIGTERMWhenRefused(t *testing.T) {
	cfg := setConfigEnv(t)
	refusingDaemon(t, cfg)
	var signalled atomic.Int64
	out, errOut, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == os.Getpid() && signalled.Load() == 0 },
		Terminate: func(pid int) error {
			signalled.Store(int64(pid))
			return nil
		},
	}, "stop", "--json")
	if err != nil {
		t.Fatalf("stop: %v (stderr %s)", err, errOut)
	}
	if signalled.Load() != int64(os.Getpid()) {
		t.Fatalf("SIGTERM went to pid %d, want the verified daemon %d", signalled.Load(), os.Getpid())
	}
	if !strings.Contains(out, `"state": "stopped"`) || !strings.Contains(errOut, "SIGTERM") {
		t.Fatalf("out %q, stderr %q", out, errOut)
	}
}

func TestStopAgentsRefusedDoesNotSignal(t *testing.T) {
	cfg := setConfigEnv(t)
	refusingDaemon(t, cfg)
	var signalled atomic.Bool
	_, _, err := executeCLI(t, Deps{
		ProcessAlive: func(pid int) bool { return pid == os.Getpid() },
		Terminate:    func(int) error { signalled.Store(true); return nil },
	}, "stop", "--stop-agents")
	if err == nil || !strings.Contains(err.Error(), "--stop-agents") || signalled.Load() {
		t.Fatalf("err %v, signalled %v; want a refusal that names --stop-agents and no signal", err, signalled.Load())
	}
}
