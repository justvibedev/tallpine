//go:build darwin || linux

package daemonsock

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// Listen creates the socket directory (0700, must be owned by us), removes a
// stale socket, binds, chmods the socket 0600 and wraps the listener so every
// connection whose peer uid differs from ours is refused. Closing the
// listener unlinks the socket.
func Listen(path string, log *slog.Logger) (net.Listener, error) {
	if len(path) > maxPathLen {
		return nil, fmt.Errorf("%w: %d bytes > %d (%s)", ErrPathTooLong, len(path), maxPathLen, path)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	if err := secureDir(dir); err != nil {
		return nil, err
	}
	if err := removeStale(path); err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("bind %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	if log == nil {
		log = slog.Default()
	}
	return &peerListener{Listener: ln, uid: os.Getuid(), log: log}, nil
}

// secureDir refuses a socket dir owned by someone else and forces it to 0700
// (MkdirAll leaves an existing dir's mode alone).
func secureDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("socket dir %s is not a directory", dir)
	}
	if uid, ok := fileUID(fi); !ok || uid != os.Getuid() {
		return fmt.Errorf("socket dir %s is not owned by this user", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil { // #nosec G302 -- a directory needs x to be entered; 0700 is owner-only.
			return fmt.Errorf("chmod socket dir: %w", err)
		}
	}
	return nil
}

// removeStale deletes a leftover socket from a crashed daemon. daemon.Run has
// already ruled out a live peer daemon; anything that is not a socket is left
// alone and makes the bind fail.
func removeStale(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	return nil
}

type peerListener struct {
	net.Listener
	uid int
	log *slog.Logger
}

// Accept returns only connections from our own uid. A peer whose credentials
// cannot be read is refused too (fail closed).
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && uid == l.uid {
			return conn, nil
		}
		l.log.Warn("daemon socket: refused peer", "peerUid", uid, "err", err)
		_ = conn.Close()
	}
}

func peerUID(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return -1, fmt.Errorf("not a unix conn: %T", conn)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, err
	}
	uid := -1
	var credErr error
	if err := raw.Control(func(fd uintptr) { uid, credErr = fdPeerUID(int(fd)) }); err != nil {
		return -1, err
	}
	return uid, credErr
}

// readUnixPeer reads the peer pid and, on macOS, the audit token. A failure is
// recorded on the Peer: the connection was already admitted by uid.
func readUnixPeer(uc *net.UnixConn) Peer {
	peer := Peer{Transport: TransportUnix}
	raw, err := uc.SyscallConn()
	if err != nil {
		peer.Err = err.Error()
		return peer
	}
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		peer.PID, peer.AuditToken, peer.HasAuditToken, credErr = fdPeerIdentity(int(fd))
	}); err != nil {
		peer.Err = err.Error()
		return peer
	}
	switch {
	case errors.Is(credErr, syscall.ENOTCONN), errors.Is(credErr, syscall.EINVAL):
		peer.Exited = true
	case credErr != nil:
		peer.Err = credErr.Error()
	}
	return peer
}
