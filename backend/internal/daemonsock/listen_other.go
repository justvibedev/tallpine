//go:build !darwin && !linux

package daemonsock

import (
	"log/slog"
	"net"
)

// Listen is unsupported here: no peer-credential check, so no socket (fail
// closed); the daemon keeps TCP only.
func Listen(string, *slog.Logger) (net.Listener, error) { return nil, ErrUnsupported }

func readUnixPeer(*net.UnixConn) Peer {
	return Peer{Transport: TransportUnix, Err: ErrUnsupported.Error()}
}
