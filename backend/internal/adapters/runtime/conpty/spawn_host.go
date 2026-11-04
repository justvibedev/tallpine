//go:build windows || darwin || linux

package conpty

import (
	"regexp"
	"time"

	"tallpine.eu/tallpine/backend/internal/hostsock"
)

// readyRE matches the "READY:<pid> <port> <token>" line printed by RunHost.
// The token is optional so a host from an older build (no auth) still parses.
var readyRE = regexp.MustCompile(`READY:(\d+) (\d+)(?: ([0-9a-f]{64}))?`)

// hostAddr builds the dial address for a host: "127.0.0.1:PORT" plus
// "#TOKEN" when the host requires authentication.
func hostAddr(port, token string) string {
	addr := "127.0.0.1:" + port
	if hostsock.IsUnix(port) {
		addr = port
	}
	if token != "" {
		addr += "#" + token
	}
	return addr
}

// readyUnixRE matches "READY:<pid> <token> unix:<path>"; the path comes last
// because it may contain spaces.
var readyUnixRE = regexp.MustCompile(`READY:(\d+) ([0-9a-f]{64}) (unix:.+)$`)

const spawnReadyTimeout = 10 * time.Second

// maxCapturedStderr bounds how much pty-host stderr we retain for diagnostics.
const maxCapturedStderr = 8192
