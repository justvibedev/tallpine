//go:build !windows && !darwin && !linux

package unixsock

import (
	"net"
	"os"
)

// No portable peer-credential API: rely on the 0600 socket mode.
func peerUID(*net.UnixConn) (int, error) { return os.Getuid(), nil }
