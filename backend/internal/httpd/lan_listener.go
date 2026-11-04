package httpd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/bodylimit"
	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
	"tallpine.eu/tallpine/backend/internal/httpd/requestscope"
	"tallpine.eu/tallpine/backend/internal/mobilebridge"
)

// LANManager owns the daemon's second, network-facing HTTP listener. It binds
// 0.0.0.0 only while Connect Mobile is enabled and wraps the shared router in
// authMiddleware. The loopback listener is unaffected.
type LANManager struct {
	handler     http.Handler // shared router, already auth-wrapped
	tunnel      http.Handler // lanTunnelPath, behind the same auth
	exchange    http.Handler // lanPairExchangePath, pre-auth by design
	pair        pairing
	defaultPort int
	log         *slog.Logger
	state       *authState // shared with authMiddleware; SetPasswordHash writes through here
	lock        *lockout

	transitionMu sync.Mutex // serializes Start and Stop without blocking status reads
	mu           sync.Mutex
	srv          *http.Server
	ln           *lanListener
	tunnelLn     *lanListener // loopback-only, for cloudflared; see clientSource
	cancel       context.CancelFunc
	bound        int
	bindHost     string
	tlsCfg       *tls.Config // nil: plain http only
}

// NewLANManager wraps handler in the LAN control-block and authMiddleware
// (backed by the shared state) and returns a manager that can start/stop the
// network-facing listener. Most callers want NewMobileLAN, which owns the state.
func NewLANManager(handler http.Handler, state *authState, defaultPort int, log *slog.Logger) *LANManager {
	lock := newLockout(time.Now)
	auth := authMiddleware(state, lock)
	m := &LANManager{
		handler:     lanControlBlock(mobileLinkRewrite(auth(handler))),
		defaultPort: defaultPort,
		log:         loggerOrDefault(log),
		state:       state,
		lock:        lock,
	}
	m.tunnel = auth(http.HandlerFunc(m.serveTunnel))
	m.exchange = lanControlBlock(http.HandlerFunc(m.servePairExchange))
	return m
}

// lanControlBlock answers 404 for every request the route table (see
// lan_route_policy.go) does not classify as a mobile route, before it reaches
// auth or the shared router, as if the route were never mounted: no 403/401
// that would confirm the path exists. Some routes are gated in the shared
// router by localControlRequest, which trusts the client-supplied Host header
// and is spoofable by any remote client. The listener is the one thing a caller
// cannot spoof: it is the socket the request arrived on. So the gate is applied
// only to the network-served handler, outermost, independent of any header.
func lanControlBlock(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// chi routes on RawPath; an encoded slash would let a loopback-only route
		// classify as a mobile prefix on the decoded path.
		if strings.Contains(strings.ToLower(r.URL.RawPath), "%2f") || !lanRouteAllowed(r.Method, r.URL.Path) {
			notFoundJSON(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(requestscope.WithLAN(r.Context())))
	})
}

// isLANControlBlockedPath reports whether no method on path is reachable
// through the network listener.
func isLANControlBlockedPath(path string) bool {
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if lanRouteAllowed(m, path) {
			return false
		}
	}
	return true
}

// IsLANControlBlockedPathForTest exposes the LAN block check to package-external
// tests so route-level invariants can be asserted without a live listener.
func IsLANControlBlockedPathForTest(path string) bool { return isLANControlBlockedPath(path) }

// NewMobileLAN constructs a LANManager with its own private authState. Callers
// outside this package (the daemon) cannot construct an authState directly
// since it is unexported; this gives them a LANManager that owns one, and the
// daemon rotates the connection password exclusively via SetPasswordHash.
func NewMobileLAN(handler http.Handler, hostID string, defaultPort int, log *slog.Logger) *LANManager {
	m := NewLANManager(handler, &authState{}, defaultPort, log)
	// Outermost, ahead of auth on every entry point.
	guard := expectedHostGuard(hostID)
	m.handler, m.tunnel, m.exchange = guard(m.handler), guard(m.tunnel), guard(m.exchange)
	return m
}

// expectedHostGuard prevents a saved address from sending work to a different
// Tallpine installation after that address is reassigned. Older clients omit the
// header; auth still applies to them as before. It wraps auth, not the reverse:
// a foreign phone's bearer is unknown here, and answering 401 for it would
// tell that phone its healthy pairing was removed.
func expectedHostGuard(hostID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if expected := r.Header.Get("X-AO-Expected-Host-ID"); expected != "" && expected != hostID {
				envelope.WriteAPIError(w, r, http.StatusMisdirectedRequest, "conflict", "HOST_ID_MISMATCH", "This address belongs to another Tallpine host; reconnect to the intended host.", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SetPasswordHash arms a new pairing code hash, redeemable for PairingCodeTTL.
// It replaces the previous hash and clears lockouts inherited
// from the old code. The code only authenticates the pair-exchange route, so
// replacing it leaves paired devices connected; revoke them per device
// (CloseTokenConnections). Satisfies controllers.LANController.
func (m *LANManager) SetPasswordHash(hash string) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.state.currentHash() == hash {
		return
	}
	// The server, not the desktop's status poll, enforces the code's lifetime.
	m.state.setHashUntil(hash, m.state.clock().Add(mobilebridge.PairingCodeTTL))
	m.lock.resetAll()
}

// PasswordHash returns the current connection password hash. Used to snapshot the
// prior hash before an enable/regenerate so a failed persist can be rolled back.
// Satisfies controllers.LANController.
func (m *LANManager) PasswordHash() string {
	return m.state.currentHash()
}

// Start binds the authenticated listener on every interface for Connect Mobile.
func (m *LANManager) Start(port int) (int, error) {
	return m.start(port, "0.0.0.0")
}

// StartLoopback binds the same authenticated handler only where a local tunnel
// connector can reach it. A running listener cannot silently change bind mode.
func (m *LANManager) StartLoopback(port int) (int, error) {
	return m.start(port, "127.0.0.1")
}

// lanBindRetries and lanBindRetryDelay bound the wait for a busy gateway port.
var (
	lanBindRetries    = 6
	lanBindRetryDelay = 500 * time.Millisecond
)

func (m *LANManager) start(port int, host string) (int, error) {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	if m.srv != nil {
		defer m.mu.Unlock()
		if m.bindHost != host {
			return 0, fmt.Errorf("authenticated listener already bound to %s; disable it before changing modes", m.bindHost)
		}
		return m.bound, nil // idempotent
	}
	if port == 0 {
		port = m.defaultPort
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	// A previous daemon may still be releasing the port; the phones' pairings
	// name it, so wait briefly before settling for another one.
	for i := 0; err != nil && isAddrInUse(err) && port != 0 && i < lanBindRetries; i++ {
		time.Sleep(lanBindRetryDelay)
		ln, err = net.Listen("tcp", addr)
	}
	if err != nil {
		if !isAddrInUse(err) {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind authenticated listener %s: %w", addr, err)
		}
		if ln, err = net.Listen("tcp", fmt.Sprintf("%s:0", host)); err != nil {
			m.mu.Unlock()
			return 0, fmt.Errorf("bind authenticated listener ephemeral on %s: %w", host, err)
		}
		m.log.Warn("LAN port in use; bound ephemeral", "wanted", port, "bound", ln.Addr())
	}
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		m.mu.Unlock()
		_ = ln.Close()
		return 0, fmt.Errorf("bind LAN: unexpected listener address type %T", ln.Addr())
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	var inner net.Listener
	if m.tlsCfg != nil {
		sl := newSniffListener(ln, m.tlsCfg)
		sl.onHandshakeErr = func(remote net.Addr, err error) {
			m.log.Info("LAN TLS handshake failed", "remote", remote.String(), "err", err)
		}
		inner = sl
	} else {
		inner = ln
	}
	tracked := &lanListener{Listener: inner, conns: make(map[*lanConn]struct{})}
	m.ln, m.cancel = tracked, cancel
	m.bound = tcpAddr.Port
	m.bindHost = host
	m.srv = &http.Server{
		Handler:           bodylimit.BodyReadDeadline(lanBodyReadTimeout)(m.dispatch()),
		ReadHeaderTimeout: 10 * time.Second,
		// Not ReadTimeout: it would cancel SSE/WebSocket streams (see
		// bodylimit.BodyReadDeadline, which bounds body reads instead).
		IdleTimeout: lanIdleTimeout,
		// Only stream routes end on Stop (streamShutdownMiddleware); the base
		// context stays live so other in-flight requests drain.
		BaseContext: func(net.Listener) context.Context {
			return context.WithValue(context.Background(), eventStreamsShutdownKey{}, streamCtx.Done())
		},
		ConnContext: func(ctx context.Context, conn net.Conn) context.Context {
			if lc, ok := conn.(*lanConn); ok && lc.owner.cloudflared {
				ctx = context.WithValue(ctx, cloudflaredContextKey{}, true)
			}
			if lc, ok := conn.(*lanConn); ok {
				_, isTLS := lc.Conn.(*tls.Conn)
				ctx = context.WithValue(ctx, lanTLSContextKey{}, isTLS)
			}
			return context.WithValue(ctx, lanConnContextKey{}, conn)
		},
	}
	// HTTP/2 without TLS (prior knowledge) lets a desktop carry a remote
	// preview's hundreds of module requests over one connection; over HTTP/1.1
	// each waits its turn on a handful of connections, a round trip apiece.
	// HTTP/1.1 clients and WebSocket upgrades are unaffected.
	m.srv.Protocols = new(http.Protocols)
	m.srv.Protocols.SetHTTP1(true)
	m.srv.Protocols.SetUnencryptedHTTP2(true)
	srv := m.srv
	boundPort := m.bound
	m.mu.Unlock()
	go func() {
		if err := srv.Serve(tracked); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.log.Error("LAN listener serve", "err", err)
		}
	}()
	m.log.Info("LAN listener started", "addr", ln.Addr())
	return boundPort, nil
}

// Stop ends stream routes and drains the listener within ctx. It
// closes remaining connections, including hijacked WebSockets, before clearing
// the bound state. A grace-period error is returned after cleanup completes.
func (m *LANManager) Stop(ctx context.Context) error {
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	m.mu.Lock()
	srv, ln, tunnelLn, cancel := m.srv, m.ln, m.tunnelLn, m.cancel
	m.mu.Unlock()
	if srv == nil {
		return nil
	}
	cancel()
	err := srv.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, srv.Close())
	}
	// Stop may run before Serve registers its listener with http.Server.
	// Close our listener explicitly even when Shutdown had nothing to close.
	err = errors.Join(err, ln.Close())
	// Shutdown and Close deliberately leave hijacked connections alone. The
	// listener owns them until their actual Close, even after HTTP's handoff.
	err = errors.Join(err, ln.closeConnections(""))
	if tunnelLn != nil {
		err = errors.Join(err, tunnelLn.Close(), tunnelLn.closeConnections(""))
	}
	m.mu.Lock()
	m.srv, m.ln, m.tunnelLn, m.cancel, m.bound, m.bindHost = nil, nil, nil, nil, 0, ""
	m.mu.Unlock()
	return err
}

func (m *LANManager) dispatch() http.Handler {
	return lanRequestLog(m.log, m.requirePinnedTLS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == lanTunnelPath {
			m.tunnel.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == lanPairExchangePath {
			m.exchange.ServeHTTP(w, r)
			return
		}
		m.handler.ServeHTTP(w, r)
	})))
}

// lanListener tracks the lifetime of the underlying connections rather than
// HTTP states: a WebSocket leaves http.Server's registry when it is hijacked.
type lanListener struct {
	net.Listener
	mu        sync.Mutex
	conns     map[*lanConn]struct{}
	closed    bool
	closeOnce sync.Once
	closeErr  error
	// cloudflared marks the loopback listener only the tunnel connector uses.
	cloudflared bool
}

type cloudflaredContextKey struct{}

// TunnelPort is the loopback port cloudflared must connect to, or 0 when the
// dedicated listener is not up (callers then use BoundPort).
func (m *LANManager) TunnelPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tunnelLn == nil {
		return 0
	}
	if a, ok := m.tunnelLn.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// OpenTunnelPort binds cloudflared's own loopback port (idempotent) and
// returns it, or 0 when the LAN listener is down or the bind fails (the tunnel
// then falls back to the main port: every visitor counts as one loopback
// source, safe but coarser). CF-Connecting-IP is believed only on connections
// accepted there, never from other loopback peers such as Tailscale Serve.
// It exists only while the quick tunnel is enabled; see CloseTunnelPort.
func (m *LANManager) OpenTunnelPort() int {
	m.mu.Lock()
	if m.srv == nil {
		m.mu.Unlock()
		return 0
	}
	if m.tunnelLn == nil {
		tl, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			m.mu.Unlock()
			m.log.Warn("tunnel listener bind failed; tunnel uses the main port", "err", err)
			return 0
		}
		m.tunnelLn = &lanListener{Listener: tl, conns: make(map[*lanConn]struct{}), cloudflared: true}
		tracked, srv := m.tunnelLn, m.srv
		go func() {
			if err := srv.Serve(tracked); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				m.log.Error("tunnel listener serve", "err", err)
			}
		}()
	}
	m.mu.Unlock()
	return m.TunnelPort()
}

// CloseTunnelPort closes the cloudflared loopback listener and its connections.
func (m *LANManager) CloseTunnelPort() {
	m.mu.Lock()
	tl := m.tunnelLn
	m.tunnelLn = nil
	m.mu.Unlock()
	if tl != nil {
		_ = tl.Close()
		_ = tl.closeConnections("")
	}
}

func (l *lanListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = conn.Close()
		return nil, net.ErrClosed
	}
	tracked := &lanConn{Conn: conn, owner: l}
	l.conns[tracked] = struct{}{}
	return tracked, nil
}

func (l *lanListener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

// An empty tokenHash closes every connection; otherwise only
// connections authenticated with that token hash are closed.
func (l *lanListener) closeConnections(tokenHash string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for conn := range l.conns {
		if tokenHash != "" && conn.tokenHash != tokenHash {
			continue
		}
		// Keep selection and TCP close atomic with credential recording. Calling
		// lanConn.Close here would re-enter mu; close its underlying socket instead.
		if err := conn.Conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
		delete(l.conns, conn)
	}
	return errors.Join(errs...)
}

type lanConnContextKey struct{}

type lanConn struct {
	net.Conn
	owner *lanListener
	// Last authenticated credential, guarded by owner.mu; includes hijacked streams.
	tokenHash string
}

// Preserve the TCP capabilities net/http uses for response copying and sending
// a FIN before closing connections with unread request bodies.
func (c *lanConn) ReadFrom(r io.Reader) (int64, error) { return io.Copy(c.Conn, r) }

func (c *lanConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return nil
}

func (c *lanConn) Close() error {
	err := c.Conn.Close()
	c.owner.mu.Lock()
	delete(c.owner.conns, c)
	c.owner.mu.Unlock()
	return err
}

// Running reports whether the LAN listener is currently serving.
func (m *LANManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.srv != nil
}

// BoundPort returns the port the listener is bound to, or 0 when not running.
func (m *LANManager) BoundPort() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound
}

const (
	// lanBodyReadTimeout bounds delivery of one request body on LAN and tunnel
	// connections; the largest legitimate body is a ~130 MiB attachment upload.
	lanBodyReadTimeout = 2 * time.Minute
	lanIdleTimeout     = 5 * time.Minute
)
