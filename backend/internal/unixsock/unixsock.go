//go:build !windows

// Package unixsock creates owner-only Unix domain socket listeners and
// rejects connections from other local users.
package unixsock

import (
	"net"
	"os"
	"sync"
	"syscall"
)

// umaskMu serialises the process-wide umask change around bind so concurrent
// listeners (and the restore) cannot interleave.
var umaskMu sync.Mutex

// afterBind is a test seam invoked between bind and chmod.
var afterBind func(path string)

// ListenPrivate binds a Unix socket at path with mode 0600 from the moment it
// exists (umask 077 around bind), then wraps it so that connections from any
// other UID are closed on accept. The explicit chmod covers platforms where
// bind ignores the umask.
func ListenPrivate(path string) (net.Listener, error) {
	umaskMu.Lock()
	old := syscall.Umask(0o077)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	umaskMu.Unlock()
	if err != nil {
		return nil, err
	}
	if afterBind != nil {
		afterBind(path)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return NewOwnerOnly(ln, os.Getuid()), nil
}

// NewOwnerOnly wraps ln so Accept silently drops connections whose peer UID
// differs from uid. A peer whose credentials cannot be read is dropped too.
func NewOwnerOnly(ln net.Listener, uid int) net.Listener {
	return &ownerOnlyListener{Listener: ln, uid: uid}
}

type ownerOnlyListener struct {
	net.Listener
	uid int
}

func (l *ownerOnlyListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if peerAllowed(conn, l.uid) {
			return conn, nil
		}
		_ = conn.Close()
	}
}

func peerAllowed(conn net.Conn, uid int) bool {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return false
	}
	peer, err := peerUID(uc)
	return err == nil && peer == uid
}

// PeerUID returns the UID of the process on the other end of c.
func PeerUID(c *net.UnixConn) (int, error) { return peerUID(c) }
