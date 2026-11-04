// Package hostsock is the local transport for detached session hosts (the
// conpty pty-host and the persistent chat host). A host listens on a 0600
// Unix socket inside a 0700 directory under the data dir instead of a
// loopback TCP port, so other local users cannot even connect; the per-host
// token handshake stays on top as defense in depth.
//
// Addresses are published as "unix:<path>". Anything else is a legacy
// "host:port" TCP address from an older build, which clients still dial but
// no host listens on any more.
package hostsock

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// Scheme prefixes a Unix-socket host address.
const Scheme = "unix:"

// DirName is the per-user socket directory under the data dir.
const DirName = "hostsock"

// maxPathBytes keeps sockaddr_un valid on macOS (104 bytes incl. NUL) and Linux.
const maxPathBytes = 103

// IsUnix reports whether addr names a Unix socket rather than a legacy TCP port.
func IsUnix(addr string) bool { return strings.HasPrefix(addr, Scheme) }

// Path returns the socket path of a Unix address.
func Path(addr string) string { return strings.TrimPrefix(addr, Scheme) }

// DirEnv is the environment variable through which the daemon tells a spawned
// host which directory to bind in (its own env may be scrubbed).
func DirEnv() string { return brand.Env("HOST_SOCK_DIR") }

var (
	dirMu      sync.RWMutex
	registered string
)

// SetDir records the socket directory derived from the daemon's resolved
// (absolute) data dir. The daemon calls it once config is loaded, so spawned
// hosts get that exact path rather than a second reading of the environment.
func SetDir(dataDir string) {
	dirMu.Lock()
	registered = filepath.Join(dataDir, DirName)
	dirMu.Unlock()
}

// DefaultDir resolves the socket directory: the daemon-registered one, else
// the DirEnv handed to a spawned host, else the data-dir override or default.
// The result is always absolute, so a host started in a session worktree never
// creates the directory inside the user's repo.
func DefaultDir() (string, error) {
	dirMu.RLock()
	dir := registered
	dirMu.RUnlock()
	if dir == "" {
		dir = os.Getenv(DirEnv())
	}
	if dir == "" {
		if v := os.Getenv(brand.Env("DATA_DIR")); v != "" {
			dir = filepath.Join(v, DirName)
		}
	}
	if dir == "" {
		home, err := brand.HomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, "data", DirName)
	}
	return filepath.Abs(dir)
}

// Dial connects to a "unix:" address. Callers send the token handshake.
func Dial(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	return dial(ctx, addr, timeout)
}

// aliasPrefix names the short /tmp symlinks that stand in for a long dir.
// Names are "<prefix><pid>-<hex16>"; the pid-based sweep must never see the
// legacy uid-named "-hs-" aliases, hence the distinct prefix.
func aliasPrefix() string { return brand.CLIName + "-hsp-" }

// legacyAliasPrefix names aliases made before the shared pid-named helper
// ("<prefix><uid>-<hex>"); they are swept by age and socket liveness.
func legacyAliasPrefix() string { return brand.CLIName + "-hs-" }
