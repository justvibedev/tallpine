//go:build darwin || linux

package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonmeta"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/runfile"
)

// B176: a live predecessor serving only its socket (TCP closed by default)
// must still be recognized, or a second daemon would start over it.
func TestStaleProbeReachesSocketOnlyPredecessor(t *testing.T) {
	dir, err := os.MkdirTemp("", "sp") // short: sun_path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := daemonsock.Path(filepath.Join(dir, "running.json"))
	ln, err := daemonsock.Listen(sock, nil)
	if err != nil {
		t.Fatal(err)
	}
	const pid = 4242
	srv := &http.Server{Handler: healthzBody(daemonmeta.ServiceName, pid), ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	// Port 1: nothing answers on TCP, as with a socket-only daemon.
	info := &runfile.Info{PID: pid, Port: 1, Socket: sock}
	if !runFileOwnerServing(staleProbeClient(info), config.LoopbackHost, info) {
		t.Fatal("a predecessor serving its socket was treated as stale")
	}
}
