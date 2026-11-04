//go:build !windows

package browserstream

import (
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"tallpine.eu/tallpine/backend/internal/unixsock"
)

// Listen creates a private, short-lived Unix socket for the Electron media
// host in the run-file's directory, reached through a short alias in /tmp. The
// token still authenticates the peer, and connections from other UIDs are
// dropped.
func Listen(runFilePath string) (net.Listener, string, error) {
	return listenAt(unixsock.ShortAliasRoot(), runFilePath)
}

func listenAt(aliasRoot, runFilePath string) (net.Listener, string, error) {
	sweepLegacyDirs(filepath.Dir(runFilePath), unixsock.ProcessAlive)
	return unixsock.ListenAliasedAt(aliasRoot, filepath.Dir(runFilePath), "tallpine-bs-", "s.sock")
}

var legacyDirPattern = regexp.MustCompile(`^bstream-(\d+)-`)

// sweepLegacyDirs removes the per-listen private dirs "bstream-<pid>-*" that
// older builds left behind when their daemon died. (Their /tmp aliases share
// the current prefix and are swept by unixsock.)
func sweepLegacyDirs(dataDir string, alive func(int) bool) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		m := legacyDirPattern.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		if pid, _ := strconv.Atoi(m[1]); pid > 0 && !alive(pid) {
			_ = os.RemoveAll(filepath.Join(dataDir, e.Name()))
		}
	}
}
