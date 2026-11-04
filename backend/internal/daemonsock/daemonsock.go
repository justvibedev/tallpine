// Package daemonsock is the user-only Unix socket the desktop app, the CLI and
// agent hooks use to reach the daemon instead of loopback TCP (B170).
//
// The socket lives in a 0700 directory beside the run-file (under the state
// root), is chmod 0600 after bind, and every accepted connection's peer uid
// must equal the daemon's uid or it is closed (fail closed). The HTTP handler
// chain, including the X-VO-Token bearer check, is identical to the TCP one.
// Windows and Unixes without a peer-credential check keep TCP only.
package daemonsock

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"time"
)

// DirName and FileName locate the socket: <dir(run-file)>/sock/daemon.sock.
const (
	DirName  = "sock"
	FileName = "daemon.sock"
)

// maxPathLen is the usable sun_path length (104 on macOS incl. NUL, 108 on
// Linux); the smaller one keeps behaviour identical on both.
const maxPathLen = 103

// ErrUnsupported means this platform serves no daemon socket (Windows, or a
// Unix without a peer-credential check); callers keep using TCP.
var ErrUnsupported = errors.New("daemon socket unsupported on this platform")

// ErrPathTooLong means the socket path exceeds sun_path; the daemon keeps TCP.
var ErrPathTooLong = errors.New("daemon socket path too long")

// Path returns the socket path for a run-file path.
func Path(runFilePath string) string {
	return filepath.Join(filepath.Dir(runFilePath), DirName, FileName)
}

// Transport returns an HTTP transport that dials the daemon socket for every
// request, whatever the URL host. Proxy is nil so HTTP(S)_PROXY can never
// receive the daemon token.
func Transport(socketPath string) *http.Transport {
	var d net.Dialer
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return d.DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:    4,
		IdleConnTimeout: 30 * time.Second,
	}
}

// LoopbackTransport is the TCP fallback for daemons without a socket. Proxy
// is nil for the same reason as Transport.
func LoopbackTransport() *http.Transport {
	return &http.Transport{Proxy: nil, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
}
