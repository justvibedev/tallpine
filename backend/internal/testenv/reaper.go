package testenv

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// e2eBinPrefix is the temp-dir prefix the e2e suites build their `tallpine` binary
// into (backend/e2e, backend/internal/cli). A daemon running from such a dir
// whose parent test process is gone is an orphan: a killed or timed-out
// `go test` never reaches its teardown.
const e2eBinPrefix = "tallpine-e2e-bin"

// ownerFile inside a bin dir holds the pid of the test process that built it.
const ownerFile = "owner.pid"

// graceNoOwner protects a bin dir whose owner file is not written yet.
const graceNoOwner = 2 * time.Minute

// MkdirE2EBin creates a tallpine-e2e-bin* dir under the temp dir and records the
// calling test process as its owner, so later runs reap its processes only
// once that owner is dead (never merely because they were reparented to init:
// persistent chat hosts outlive a daemon stop by design).
func MkdirE2EBin() (string, error) {
	dir, err := os.MkdirTemp("", e2eBinPrefix)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, ownerFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// ownerAlive reports whether the bin dir's owner must be assumed running:
// a live owner pid, or no owner file yet on a fresh dir.
func ownerAlive(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, ownerFile)) //nolint:gosec // test-temp path.
	if err != nil {
		fi, serr := os.Stat(dir)
		return serr == nil && time.Since(fi.ModTime()) < graceNoOwner
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return false
	}
	return pidAlive(pid)
}

// staleE2EDaemon is one process considered for reaping.
type staleE2EDaemon struct {
	pid, ppid int
	exe       string
}

// tempRoots lists the spellings of the temp dir (macOS /var vs /private/var).
func tempRoots() []string {
	roots := []string{filepath.Clean(os.TempDir())}
	if r, err := filepath.EvalSymlinks(roots[0]); err == nil && r != roots[0] {
		roots = append(roots, r)
	}
	return roots
}

// staleDaemons parses `ps -axo pid=,ppid=,command=` output.
func staleDaemons(psOut string, roots []string) []staleE2EDaemon {
	var found []staleE2EDaemon
	for _, line := range strings.Split(psOut, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || ppid != 1 || pid == os.Getpid() {
			continue
		}
		exe := f[2]
		dir := filepath.Dir(exe)
		if filepath.Base(exe) != "tallpine" || !strings.HasPrefix(filepath.Base(dir), e2eBinPrefix) {
			continue
		}
		for _, r := range roots {
			if filepath.Dir(dir) == r && !ownerAlive(dir) {
				found = append(found, staleE2EDaemon{pid: pid, ppid: ppid, exe: exe})
				break
			}
		}
	}
	return found
}
