package httpd

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// newConns tracks connections that were accepted but have not sent a request.
// http.Server.Shutdown treats such StateNew conns as active until they are 5s
// old, so a silent local client would delay daemon shutdown by that long (B343).
type newConns struct {
	mu    sync.Mutex
	conns map[net.Conn]struct{}
}

// track is the http.Server.ConnState hook.
func (n *newConns) track(c net.Conn, st http.ConnState) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if st == http.StateNew {
		if n.conns == nil {
			n.conns = map[net.Conn]struct{}{}
		}
		n.conns[c] = struct{}{}
		return
	}
	delete(n.conns, c)
}

func (n *newConns) closeAll() {
	n.mu.Lock()
	defer n.mu.Unlock()
	for c := range n.conns {
		_ = c.Close()
	}
}

// closeWhile closes never-active conns periodically until stop is closed.
// Requests already in flight (StateActive) are untouched and still drain.
func (n *newConns) closeWhile(stop <-chan struct{}) {
	t := time.NewTicker(25 * time.Millisecond)
	defer t.Stop()
	for {
		n.closeAll()
		select {
		case <-stop:
			return
		case <-t.C:
		}
	}
}
