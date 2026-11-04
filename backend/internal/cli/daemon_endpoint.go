package cli

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
)

// loopbackDaemonTransport serves daemons that advertise no socket (Windows,
// older daemons). Proxy is nil so HTTP(S)_PROXY never sees the daemon token.
var loopbackDaemonTransport = daemonsock.LoopbackTransport()

// socketTransports caches one transport per socket path so long-lived CLI
// processes (tallpine mcp, review wait) reuse keep-alive connections instead of
// leaving one idle connection behind per call.
var socketTransports sync.Map // socket path -> *http.Transport

func socketTransport(socket string) *http.Transport {
	if v, ok := socketTransports.Load(socket); ok {
		if t, ok := v.(*http.Transport); ok {
			return t
		}
	}
	v, _ := socketTransports.LoadOrStore(socket, daemonsock.Transport(socket))
	if t, ok := v.(*http.Transport); ok {
		return t
	}
	return daemonsock.Transport(socket)
}

// daemonEndpoint returns the base URL and client for one daemon call. When
// running.json advertises the user-only socket (B170) every call goes there
// otherwise it falls back to loopback TCP. A test-injected transport is kept
// for TCP. Over the socket the URL still names 127.0.0.1:<port>: the daemon
// builds render and preview URLs from the request Host, and that is the
// origin the desktop app serves them on even with TCP closed (B176).
func (c *commandContext) daemonEndpoint(socket string, port int, timeout time.Duration) (string, *http.Client) {
	client := *c.deps.HTTPClient
	client.Timeout = timeout // 0 = no client timeout (long setup calls)
	if socket != "" {
		client.Transport = socketTransport(socket)
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
