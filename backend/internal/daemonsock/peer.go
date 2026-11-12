package daemonsock

import (
	"context"
	"net"
)

// PeerTransport names how a caller reached the daemon's HTTP handler chain.
type PeerTransport string

// Transport values for Peer.Transport.
const (
	TransportUnix PeerTransport = "unix"
	TransportTCP  PeerTransport = "tcp"
)

// Peer is what the kernel says about the process on the other end of an
// accepted connection. TCP has no such facts. Err explains an unreadable
// peer; callers must treat it as unidentified, never as trusted.
type Peer struct {
	Transport PeerTransport
	PID       int
	// AuditToken is the Mach audit token, which unlike a pid cannot name a
	// later process. Set only when HasAuditToken is true (macOS).
	AuditToken    [8]uint32
	HasAuditToken bool
	// Exited means the peer had already closed the connection when it was
	// read; the kernel keeps no credentials for it.
	Exited bool
	Err    string
}

type peerContextKey struct{}

// ContextWithConn stores the peer of conn on ctx. It is the http.Server
// ConnContext hook, so the peer is read once per accepted connection.
func ContextWithConn(ctx context.Context, conn net.Conn) context.Context {
	return context.WithValue(ctx, peerContextKey{}, connPeer(conn))
}

// ContextWithPeer stores a known peer on ctx, as ContextWithConn does for an
// accepted connection.
func ContextWithPeer(ctx context.Context, peer Peer) context.Context {
	return context.WithValue(ctx, peerContextKey{}, peer)
}

// PeerFrom returns the peer stored by ContextWithConn.
func PeerFrom(ctx context.Context) (Peer, bool) {
	peer, ok := ctx.Value(peerContextKey{}).(Peer)
	return peer, ok
}

// WithPeerFrom copies the peer stored on src onto ctx, for work that runs under
// a fresh context but on behalf of src's connection.
func WithPeerFrom(ctx, src context.Context) context.Context {
	if peer, ok := PeerFrom(src); ok {
		return context.WithValue(ctx, peerContextKey{}, peer)
	}
	return ctx
}

func connPeer(conn net.Conn) Peer {
	if uc, ok := conn.(*net.UnixConn); ok {
		return readUnixPeer(uc)
	}
	return Peer{Transport: TransportTCP}
}
