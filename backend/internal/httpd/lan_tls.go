package httpd

import (
	"bufio"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/httpd/envelope"
)

// sniffTimeout bounds how long a fresh connection may stay silent before it is
// dropped; a TLS or HTTP client speaks first.
const sniffTimeout = 5 * time.Second

// tlsRecordHandshake is the first byte of a TLS ClientHello.
const tlsRecordHandshake = 0x16

// sniffListener serves TLS and plain http on one port, so the gateway port the
// phones already know stays stable. A connection whose first byte is a TLS
// handshake is wrapped in TLS; anything else is plain http. Whether plain http
// is still served to a given peer is decided per request (requirePinnedTLS), so
// a refused phone gets a readable answer instead of a dropped connection.
type sniffListener struct {
	net.Listener
	cfg *tls.Config
	// onHandshakeErr reports a failed TLS handshake (nil: ignore).
	onHandshakeErr func(remote net.Addr, err error)

	once  sync.Once
	conns chan net.Conn
	errc  chan error
	done  chan struct{}
}

func newSniffListener(ln net.Listener, cfg *tls.Config) *sniffListener {
	return &sniffListener{
		Listener: ln,
		cfg:      cfg,
		conns:    make(chan net.Conn),
		errc:     make(chan error),
		done:     make(chan struct{}),
	}
}

func (s *sniffListener) Accept() (net.Conn, error) {
	s.once.Do(func() { go s.acceptLoop() })
	select {
	case c := <-s.conns:
		return c, nil
	case err := <-s.errc:
		return nil, err
	case <-s.done:
		return nil, net.ErrClosed
	}
}

func (s *sniffListener) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return s.Listener.Close()
}

func (s *sniffListener) acceptLoop() {
	for {
		c, err := s.Listener.Accept()
		if err != nil {
			// Temporary errors (EMFILE, ECONNABORTED) are retried by http.Server,
			// so report them and keep accepting; only a permanent error ends us.
			t, ok := err.(interface{ Temporary() bool })
			temporary := ok && t.Temporary() && !errors.Is(err, net.ErrClosed)
			select {
			case s.errc <- err:
			case <-s.done:
				return
			}
			if !temporary {
				return
			}
			continue
		}
		go s.classify(c)
	}
}

func (s *sniffListener) classify(c net.Conn) {
	_ = c.SetReadDeadline(time.Now().Add(sniffTimeout))
	br := bufio.NewReaderSize(c, 16)
	first, err := br.Peek(1)
	_ = c.SetReadDeadline(time.Time{})
	if err != nil {
		// Silent or idle connections (port scans, reachability probes) are not failures worth logging.
		_ = c.Close()
		return
	}
	var out net.Conn = &peekedConn{Conn: c, r: br}
	if first[0] == tlsRecordHandshake {
		tc := tls.Server(out, s.cfg)
		// Handshake here, not lazily in the http server: net/http only logs
		// handshake failures for a bare *tls.Conn, and ours is wrapped, so a
		// phone rejecting the certificate would otherwise leave no trace.
		_ = c.SetDeadline(time.Now().Add(sniffTimeout))
		if err := tc.Handshake(); err != nil {
			if s.onHandshakeErr != nil {
				s.onHandshakeErr(c.RemoteAddr(), err)
			}
			_ = c.Close()
			return
		}
		_ = c.SetDeadline(time.Time{})
		out = tc
	}
	select {
	case s.conns <- out:
	case <-s.done:
		_ = c.Close()
	}
}

// peekedConn replays the byte the classifier read.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func isLoopbackAddr(a net.Addr) bool {
	t, ok := a.(*net.TCPAddr)
	return ok && t.IP.IsLoopback()
}

// SetTLS makes the gateway serve TLS with cert. Plain http from non-loopback
// peers is refused with 426 (see requirePinnedTLS); there is no migration
// window. It must be called before Start.
func (m *LANManager) SetTLS(cert tls.Certificate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tlsCfg = &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
}

// TLSEnabled reports whether the gateway terminates TLS.
func (m *LANManager) TLSEnabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tlsCfg != nil
}

// lanTLSContextKey marks requests that arrived on a connection the gateway
// itself terminated TLS on; those are encrypted whatever the peer address.
type lanTLSContextKey struct{}

// requirePinnedTLS answers 426 to every plain-http request from the network,
// on every route the listener serves (API, chat streams, tunnel, pairing). It
// does not depend on TLS being configured: when the gateway key could not be
// created the listener fails closed rather than serving plain http. Loopback
// peers (Tailscale Serve, cloudflared) stay plain; they terminate TLS
// themselves. The body is static and the error code is what the phone maps to
// its "re-pair for secure connection" message.
func (m *LANManager) requirePinnedTLS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isTLS, _ := r.Context().Value(lanTLSContextKey{}).(bool); !isTLS && !loopbackPeer(r) {
			envelope.WriteAPIError(w, r, http.StatusUpgradeRequired, "upgrade_required", "HTTPS_REQUIRED",
				"re-pair this phone for a secure connection", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loopbackPeer reports whether the socket peer is loopback. An unparsable
// address is treated as remote.
func loopbackPeer(r *http.Request) bool {
	tcp, err := net.ResolveTCPAddr("tcp", r.RemoteAddr)
	return err == nil && isLoopbackAddr(tcp)
}
