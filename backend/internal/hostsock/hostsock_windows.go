//go:build windows

package hostsock

import (
	"context"
	"errors"
	"net"
	"time"
)

// Supported is false on Windows: hosts keep their loopback TCP transport.
const Supported = false

var errUnsupported = errors.New("hostsock: unix sockets are not used on windows")

// Listen is unavailable on Windows.
func Listen(string) (net.Listener, string, error) { return nil, "", errUnsupported }

func dial(context.Context, string, time.Duration) (net.Conn, error) { return nil, errUnsupported }
