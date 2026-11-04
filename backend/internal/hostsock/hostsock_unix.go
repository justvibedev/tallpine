//go:build !windows

package hostsock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"tallpine.eu/tallpine/backend/internal/unixsock"
)

// Supported reports whether hosts can listen on a Unix socket here.
const Supported = true

// aliasRoot and currentUID are test seams.
var (
	aliasRoot  = unixsock.ShortAliasRoot()
	currentUID = os.Getuid
)

// Listen binds a fresh private socket in dir (created 0700) and returns the
// listener plus the "unix:" address to publish. When the path would exceed
// sun_path, a random symlink under /tmp points at dir and the address goes
// through it. Closing the listener removes the socket and the symlink.
func Listen(dir string) (net.Listener, string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, "", err
	}
	if err := ensurePrivateDir(dir); err != nil {
		return nil, "", err
	}
	sweepStale(dir)
	name, err := randHex(8)
	if err != nil {
		return nil, "", err
	}
	sock := name + ".sock"
	if len(filepath.Join(dir, sock)) > maxPathBytes {
		// The shared hardened aliased listener keeps the bind path in sun_path.
		ln, path, err := unixsock.ListenAliasedAt(aliasRoot, dir, aliasPrefix(), sock)
		if err != nil {
			return nil, "", err
		}
		return ln, Scheme + path, nil
	}
	path := filepath.Join(dir, sock)
	ln, err := unixsock.ListenPrivate(path)
	if err != nil {
		return nil, "", err
	}
	return &cleanupListener{Listener: ln, path: path}, Scheme + path, nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || ownerOf(info) != currentUID() {
		return fmt.Errorf("host socket dir %s is not a directory owned by the current user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700) //nolint:gosec // directory, owner-only
	}
	return nil
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ownerOf(info os.FileInfo) int {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// dial refuses any socket (or /tmp alias leading to it) not owned by the
// current user, then checks the connected peer's UID as well, so a squatted
// symlink or a socket bound by another user is never trusted.
func dial(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	path := Path(addr)
	if err := verifyOwner(path); err != nil {
		return nil, err
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if uc, ok := conn.(*net.UnixConn); ok {
		if uid, perr := unixsock.PeerUID(uc); perr != nil || uid != currentUID() {
			_ = conn.Close()
			return nil, fmt.Errorf("host socket %s: peer is not the current user", path)
		}
	}
	return conn, nil
}

func verifyOwner(path string) error {
	dir := filepath.Dir(path)
	if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 && ownerOf(info) != currentUID() {
		return fmt.Errorf("host socket alias %s is not owned by the current user", dir)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("host socket %s is not a socket", path)
	}
	if ownerOf(info) != currentUID() {
		return fmt.Errorf("host socket %s is not owned by the current user", path)
	}
	return nil
}

// sweepStale removes sockets of dead hosts (killed without cleanup) and the
// /tmp aliases whose owning process is gone.
func sweepStale(dir string) {
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".sock") {
				continue
			}
			p := filepath.Join(dir, e.Name())
			c, err := net.DialTimeout("unix", p, 200*time.Millisecond)
			if err == nil {
				_ = c.Close()
			} else if errors.Is(err, syscall.ECONNREFUSED) {
				_ = os.Remove(p)
			}
		}
	}
	_ = unixsock.CleanupStaleAliases(aliasRoot, aliasPrefix())
	sweepLegacyAliases(dir)
}

// legacyAliasMinAge is a test seam.
var legacyAliasMinAge = 30 * time.Second

// sweepLegacyAliases removes uid-named aliases of older builds that point at
// dir but no longer lead to a live socket.
func sweepLegacyAliases(dir string) {
	entries, err := os.ReadDir(aliasRoot)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), legacyAliasPrefix()) || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		// A host between alias creation and bind has no socket yet; leave new aliases.
		if fi, err := e.Info(); err != nil || time.Since(fi.ModTime()) < legacyAliasMinAge {
			continue
		}
		a := filepath.Join(aliasRoot, e.Name())
		if t, err := os.Readlink(a); err != nil || filepath.Clean(t) != dir {
			continue
		}
		if m, _ := filepath.Glob(filepath.Join(a, "*.sock")); len(m) == 0 {
			_ = os.Remove(a)
		}
	}
}

type cleanupListener struct {
	net.Listener
	path string
	once sync.Once
}

func (l *cleanupListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() {
		_ = os.Remove(l.path)
	})
	return err
}
