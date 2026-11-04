package testenv

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tmuxBaseDir is where Setup creates the tmux socket dir. It is /tmp, not
// os.TempDir(): macOS's long $TMPDIR leaves too little of the 104-byte
// sun_path. Setup and the reaper must share it.
//
// It is a dedicated subdir, not /tmp itself, so the protectState sandbox can
// allow exactly this path (the claudecode sandbox allowWrite list) without opening all of /tmp.
const tmuxBaseDir = "/tmp/tp-tmux"

// mkTmuxDir creates a tp-tmux-* dir under tmuxBaseDir owned by this process.
func mkTmuxDir() (string, error) {
	if err := os.MkdirAll(tmuxBaseDir, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(tmuxBaseDir, tmuxDirPrefix)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// tmuxDirPrefix is the prefix of the private TMUX_TMPDIR Setup creates under
// /tmp. The dir holds an owner.pid file naming the test process.
const tmuxDirPrefix = "tp-tmux-"

// staleTmuxDirs lists tp-tmux-* dirs under root whose owner test process is
// dead (a killed or timed-out `go test` never reaches its cleanup).
func staleTmuxDirs(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, tmuxDirPrefix+"*"))
	var stale []string
	for _, d := range matches {
		if fi, err := os.Stat(d); err != nil || !fi.IsDir() || strings.HasSuffix(d, ownerFile) {
			continue
		}
		if !ownerAlive(d) {
			stale = append(stale, d)
		}
	}
	return stale
}

// reapStaleTmuxServers kills the tmux servers of dead test runs and removes
// their socket dirs. Only dirs this package created (tp-tmux-* with a dead
// owner) are touched, never the caller's own tmux server. Best effort.
func reapStaleTmuxServers(root string) {
	for _, d := range staleTmuxDirs(root) {
		killTmuxServers(d)
		_ = os.RemoveAll(d)
	}
}

// TmuxSocket returns a socket name unique to the calling test and kills the
// tmux server on it in t.Cleanup, so a test whose own teardown is skipped or
// fails (a Destroy error, a timeout) still leaves no server behind. Pass it as
// tmux.Options.SocketName.
func TmuxSocket(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("tp-it-%d-%d", os.Getpid(), time.Now().UnixNano()%1e9)
	t.Cleanup(func() { killTmuxSocket(name) })
	return name
}

// tmuxReapRoot is the dir Setup scans for stale tmux sockets dirs; the same
// base mkTmuxDir creates them in.
func tmuxReapRoot() string { return tmuxBaseDir }

// ShortBaseDir creates and returns the short base dir (/tmp/tp-tmux) for tests
// that need a short Unix-socket path via os.MkdirTemp. Use it instead of
// "/tmp": the protectState sandbox only lets tests write here.
func ShortBaseDir() string {
	if dir := fencedShortBaseDir(); dir != "" {
		return dir
	}
	_ = os.MkdirAll(tmuxBaseDir, 0o700)
	return tmuxBaseDir
}
