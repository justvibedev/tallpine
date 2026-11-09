package cli

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

// loopbackDaemonTransport serves daemons that advertise no socket (Windows,
// older daemons). Proxy is nil so HTTP(S)_PROXY never sees the daemon token.
var loopbackDaemonTransport = daemonsock.LoopbackTransport()

// socketTransports caches one transport per socket path and TCP fallback port so long-lived CLI
// processes (tallpine mcp, review wait) reuse keep-alive connections instead of
// leaving one idle connection behind per call.
var socketTransports sync.Map // socket path + port -> *http.Transport

func socketTransport(socket string, port int) *http.Transport {
	key := fmt.Sprintf("%s|%d", socket, port)
	if v, ok := socketTransports.Load(key); ok {
		if t, ok := v.(*http.Transport); ok {
			return t
		}
	}
	v, _ := socketTransports.LoadOrStore(key, daemonsock.FallbackTransport(socket, port))
	if t, ok := v.(*http.Transport); ok {
		return t
	}
	return daemonsock.FallbackTransport(socket, port)
}

// daemonEndpoint returns the base URL and client for one daemon call. When
// running.json advertises the user-only socket (B170) every call goes there
// otherwise it falls back to loopback TCP. A test-injected transport is kept
// for TCP. If the socket cannot be dialed (ENOENT/EPERM/EACCES) and the
// client opted in with TALLPINE_LOOPBACK_TCP=1, the dial falls back to the
// run file's port (B434). The port alone is not proof the daemon listens on TCP
// (B176), so without the opt-in the token is never sent there. Over the socket the URL still names 127.0.0.1:<port>: the daemon
// builds render and preview URLs from the request Host, and that is the
// origin the desktop app serves them on even with TCP closed (B176).
func (c *commandContext) daemonEndpoint(socket string, port int, timeout time.Duration) (string, *http.Client) {
	client := *c.deps.HTTPClient
	client.Timeout = timeout // 0 = no client timeout (long setup calls)
	if socket != "" {
		fallbackPort := 0
		if loopbackTCPOptIn() {
			fallbackPort = port
		}
		client.Transport = socketTransport(socket, fallbackPort)
		if port <= 0 {
			return "http://" + config.LoopbackHost, &client
		}
		return fmt.Sprintf("http://%s:%d", config.LoopbackHost, port), &client
	}
	if client.Transport == nil {
		client.Transport = loopbackDaemonTransport
	}
	return fmt.Sprintf("http://%s:%d", config.LoopbackHost, port), &client
}

// loopbackTCPOptIn reports whether this client's environment says the daemon
// runs with its loopback TCP listener (TALLPINE_LOOPBACK_TCP=on).
func loopbackTCPOptIn() bool {
	raw, _ := brand.LookupEnv("LOOPBACK_TCP")
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true
	}
	return false
}
