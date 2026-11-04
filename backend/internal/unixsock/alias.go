//go:build !windows

package unixsock

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"syscall"
)

// MaxPathBytes keeps sockaddr_un valid on macOS (104 bytes including the NUL)
// and Linux regardless of how long the user's data directory is.
const MaxPathBytes = 103

// randomSuffix is a test seam for the alias name's random part.
var randomSuffix = defaultRandomSuffix

func defaultRandomSuffix() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ShortAliasRoot returns the directory that holds the short symlink aliases.
// os.TempDir can inherit an arbitrarily long TMPDIR, so /tmp (normally a
// symlink to /private/tmp on macOS) is preferred.
//
// TALLPINE_SOCKET_ALIAS_ROOT overrides it (an existing absolute dir): a
// sandboxed shell (the Claude security-mode fence) may not write /tmp, so the
// merge gate points it at a dir the fence allows (B463).
func ShortAliasRoot() string {
	if d := os.Getenv("TALLPINE_SOCKET_ALIAS_ROOT"); filepath.IsAbs(d) {
		if info, err := os.Stat(d); err == nil && info.IsDir() {
			return d
		}
	}
	if info, err := os.Stat("/tmp"); err == nil && info.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

// ListenAliased binds a private (0600, owner-UID-only) socket named sockName
// inside dataDir (created 0700 if missing) through a short random symlink
// "<prefix><pid>-<16 hex>" under ShortAliasRoot, so the bind path always fits
// sun_path. It returns the listener and the aliased socket path; Close removes
// the socket and the alias. Aliases of dead processes with the same prefix are
// swept first.
func ListenAliased(dataDir, prefix, sockName string) (net.Listener, string, error) {
	return ListenAliasedAt(ShortAliasRoot(), dataDir, prefix, sockName)
}

// ListenAliasedAt is ListenAliased with an explicit alias root.
func ListenAliasedAt(aliasRoot, dataDir, prefix, sockName string) (net.Listener, string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve socket directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create socket directory: %w", err)
	}
	_ = cleanupStaleAliases(aliasRoot, prefix, ProcessAlive)

	alias, err := createAlias(aliasRoot, dir, prefix)
	if err != nil {
		return nil, "", err
	}
	sockPath := filepath.Join(alias, sockName)
	if n := len([]byte(sockPath)); n > MaxPathBytes {
		_ = os.Remove(alias)
		return nil, "", fmt.Errorf("socket path is %d bytes; maximum is %d", n, MaxPathBytes)
	}
	_ = os.Remove(filepath.Join(dir, sockName)) // leftover from a crashed run
	ln, err := ListenPrivate(sockPath)
	if err != nil {
		_ = os.Remove(alias)
		return nil, "", err
	}
	return &aliasListener{Listener: ln, socketPath: sockPath, aliasPath: alias}, sockPath, nil
}

func createAlias(root, target, prefix string) (string, error) {
	for range 16 {
		suffix, err := randomSuffix()
		if err != nil {
			return "", fmt.Errorf("generate socket alias: %w", err)
		}
		alias := filepath.Join(root, fmt.Sprintf("%s%d-%s", prefix, os.Getpid(), suffix))
		err = os.Symlink(target, alias)
		if err == nil {
			return alias, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("create socket alias: %w", err)
		}
	}
	return "", errors.New("create socket alias: exhausted random names")
}

// CleanupStaleAliases removes symlinks "<prefix><pid>-<16 hex>" under root
// whose owning process is gone (crash, SIGKILL).
func CleanupStaleAliases(root, prefix string) error {
	return cleanupStaleAliases(root, prefix, ProcessAlive)
}

func cleanupStaleAliases(root, prefix string, alive func(int) bool) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	pattern := regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + `(\d+)-[0-9a-f]{16}$`)
	for _, e := range entries {
		m := pattern.FindStringSubmatch(e.Name())
		if m == nil || e.Type()&os.ModeSymlink == 0 {
			continue
		}
		if pid, err := strconv.Atoi(m[1]); err != nil || pid <= 0 || alive(pid) {
			continue
		}
		_ = os.Remove(filepath.Join(root, e.Name()))
	}
	return nil
}

// ProcessAlive reports whether pid exists (EPERM counts as alive).
func ProcessAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

type aliasListener struct {
	net.Listener
	socketPath, aliasPath string
	once                  sync.Once
}

func (l *aliasListener) Close() error {
	err := l.Listener.Close()
	l.once.Do(func() {
		_ = os.Remove(l.socketPath)
		_ = os.Remove(l.aliasPath)
	})
	return err
}

// IsOwnerOnly reports whether ln was built by this package's private
// listeners (owner-UID filter installed).
func IsOwnerOnly(ln net.Listener) bool {
	if a, ok := ln.(*aliasListener); ok {
		ln = a.Listener
	}
	_, ok := ln.(*ownerOnlyListener)
	return ok
}
