package httpd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/daemonsock"
	"tallpine.eu/tallpine/backend/internal/daemontoken"
	previewutil "tallpine.eu/tallpine/backend/internal/preview"
	"tallpine.eu/tallpine/backend/internal/runfile"
	"tallpine.eu/tallpine/backend/internal/terminal"
)

// Server is the daemon's HTTP server together with its lifecycle: bind the
// loopback port, publish the running.json handshake, serve until the context
// is cancelled, then shut down gracefully and clean up the handshake file.
type Server struct {
	newConns newConns
	cfg      config.Config
	log      *slog.Logger
	http     *http.Server
	// listen is the loopback TCP listener: nil unless cfg.LoopbackTCP is set
	// or no socket could be bound (B176).
	listen net.Listener
	// sock is the user-only Unix socket listener (nil on Windows or when the
	// bind failed); it serves the same handler chain as listen.
	sock     net.Listener
	sockPath string

	shutdownRequested chan struct{}
	shutdownOnce      sync.Once
	// stopAgents is set by a shutdown that asked to stop every agent too.
	stopAgents atomic.Bool
}

// NewWithDeps constructs a Server with API dependencies supplied by the daemon
// and binds the listener immediately, before any running.json is written. The
// caller owns the returned Server's lifecycle via Run. termMgr may be nil, in
// which case the /mux terminal surface is not mounted.
//
// If the configured port is already held, it falls back to an OS-assigned
// ephemeral port rather than failing. A genuine peer Tallpine daemon is ruled out
// upstream (the running.json + /healthz check in daemon.Run), so a conflict here
// means a non-Tallpine process owns the port; exiting would only leave the desktop
// supervisor stuck on "daemon not ready". The actual bound port is logged
// ("daemon listening") and written to running.json, both of which the supervisor
// reads, so the fallback propagates to the renderer with no UI changes.
func NewWithDeps(cfg config.Config, log *slog.Logger, termMgr *terminal.Manager, deps APIDeps) (*Server, error) {
	log = loggerOrDefault(log)
	control := ControlDeps{}
	if cfg.DaemonAuthOffIgnored {
		log.Warn("TALLPINE_DAEMON_AUTH=off ignored: it needs TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1 and an unstamped dev build; loopback authentication stays ON")
	}
	if cfg.DaemonAuthDisabled {
		log.Warn("UNSAFE: loopback daemon authentication is OFF (TALLPINE_DAEMON_AUTH=off + TALLPINE_DAEMON_AUTH_UNSAFE_DEV=1); any local process can drive this daemon")
	} else {
		if cfg.RunFilePath == "" {
			return nil, errors.New("daemon auth: run-file path is required to locate the daemon token")
		}
		tokenPath := daemontoken.Path(cfg.RunFilePath)
		token, err := daemontoken.Ensure(tokenPath)
		if err != nil {
			return nil, fmt.Errorf("daemon auth: %w", err)
		}
		control.DaemonToken = token
		// Preview-origin capabilities derive from the token, so they survive
		// daemon restarts and rotate with it.
		previewutil.SetCapabilityKey(token)
		control.DaemonTokenPath = tokenPath
		control.RepairDaemonToken = tokenRepairer(tokenPath, token, log)
	}

	srv := &Server{
		cfg:               cfg,
		log:               log,
		shutdownRequested: make(chan struct{}),
	}
	srv.http = &http.Server{
		ConnState: srv.newConns.track,
		Handler: NewRouterWithControl(cfg, log, termMgr, deps, ControlDeps{
			RequestShutdown:      srv.requestShutdown,
			StopAgentsOnShutdown: func() { srv.stopAgents.Store(true) },
			DaemonToken:          control.DaemonToken,
			DaemonTokenPath:      control.DaemonTokenPath,
			RepairDaemonToken:    control.RepairDaemonToken,
			MobileFeatureEnabled: deps.MobileFeatureEnabled,
		}),
		// ReadHeaderTimeout guards against slow-loris even on loopback;
		// per-request body/handler timeouts are applied per-surface.
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Shutdown neither waits for nor ends hijacked connections, so the event
	// stream sockets learn of it here and close, which also ends their streams.
	streamsShutdown := make(chan struct{})
	// Stored receive-only: readers assert the value as <-chan struct{}.
	var streamsDone <-chan struct{} = streamsShutdown
	// Stream handlers end on shutdown through streamShutdownMiddleware; the
	// base context stays live so in-flight requests drain normally.
	srv.http.BaseContext = func(net.Listener) context.Context {
		return context.WithValue(context.Background(), eventStreamsShutdownKey{}, streamsDone)
	}
	srv.http.RegisterOnShutdown(func() { close(streamsShutdown) })
	if cfg.RunFilePath != "" {
		path := daemonsock.Path(cfg.RunFilePath)
		if sl, serr := daemonsock.Listen(path, log); serr == nil {
			srv.sock, srv.sockPath = sl, path
		} else if !errors.Is(serr, daemonsock.ErrUnsupported) {
			// Degrade, not crash: clients fall back to TCP when running.json
			// carries no socket.
			log.Warn("daemon socket unavailable; clients use loopback TCP", "path", path, "err", serr)
		}
	}
	// B176: with the socket up, loopback TCP stays closed unless asked for.
	// Without one the daemon would be unreachable, so TCP opens regardless.
	if cfg.LoopbackTCP || srv.sock == nil {
		ln, err := listenLoopback(cfg, log)
		if err != nil {
			if srv.sock != nil {
				_ = srv.sock.Close()
			}
			return nil, err
		}
		srv.listen = ln
	}
	return srv, nil
}

// listenLoopback binds cfg.Addr(), or an ephemeral loopback port when a non-Tallpine
// process holds the configured one (see NewWithDeps).
func listenLoopback(cfg config.Config, log *slog.Logger) (net.Listener, error) {
	ln, err := net.Listen("tcp", cfg.Addr())
	if err == nil {
		return ln, nil
	}
	if !isAddrInUse(err) {
		return nil, fmt.Errorf("bind %s: %w", cfg.Addr(), err)
	}
	// Configured port is taken by a non-Tallpine process: retry on an ephemeral port.
	fallback, ferr := net.Listen("tcp", net.JoinHostPort(cfg.Host, "0"))
	if ferr != nil {
		return nil, fmt.Errorf("bind %s (in use) and ephemeral fallback: %w", cfg.Addr(), ferr)
	}
	log.Warn("configured port in use; bound an ephemeral port instead",
		"configured", cfg.Addr(), "bound", fallback.Addr().String())
	return fallback, nil
}

// Addr returns the daemon's loopback origin: the bound TCP address (useful
// when the configured port was 0 and the OS chose one, primarily in tests),
// or, with TCP closed, the configured host:port the desktop app still names
// in daemon URLs and serves over the socket.
func (s *Server) Addr() net.Addr {
	if s.listen != nil {
		return s.listen.Addr()
	}
	return &net.TCPAddr{IP: net.ParseIP(config.LoopbackHost), Port: s.cfg.Port}
}

// ListensTCP reports whether the loopback TCP listener is open.
func (s *Server) ListensTCP() bool { return s.listen != nil }

// Handler returns the loopback server's built router so the daemon can share
// the exact same handler instance with the LAN listener (via NewMobileLAN),
// keeping the loopback and LAN surfaces identical.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// Run serves until ctx is cancelled (SIGINT/SIGTERM via signal.NotifyContext),
// then performs a graceful shutdown bounded by cfg.ShutdownTimeout. It writes
// running.json before serving and removes it on the way out. Run blocks until
// shutdown is complete.
func (s *Server) Run(ctx context.Context) error {
	return s.run(ctx, nil)
}

// RunWithReady is Run with a callback invoked after the listener has been
// published and its serving goroutine has started. The callback must return
// promptly; it is intended for boot work that can safely continue while the
// daemon serves read-only durable state.
func (s *Server) RunWithReady(ctx context.Context, onReady func()) error {
	return s.run(ctx, onReady)
}

func (s *Server) run(ctx context.Context, onReady func()) error {
	info := runfile.Info{
		PID:                   os.Getpid(),
		Port:                  s.boundPort(),
		Socket:                s.sockPath,
		StartedAt:             time.Now().UTC(),
		Owner:                 brand.Getenv("OWNER"),
		AppRunID:              s.cfg.AppRunID,
		BrowserRuntimeAddress: brand.Getenv("BROWSER_RUNTIME_ADDRESS"),
		BrowserStreamAddress:  brand.Getenv("BROWSER_STREAM_ADDRESS"),
	}
	if err := runfile.Write(s.cfg.RunFilePath, info); err != nil {
		if s.listen != nil {
			_ = s.listen.Close()
		}
		if s.sock != nil {
			_ = s.sock.Close()
		}
		return fmt.Errorf("write run-file: %w", err)
	}
	defer func() {
		if err := runfile.RemoveIfOwned(s.cfg.RunFilePath, info.PID); err != nil {
			s.log.Warn("failed to remove run-file", "path", s.cfg.RunFilePath, "err", err)
		}
	}()

	// The first listener to stop on its own (a real runtime failure, since the
	// bind already happened) ends Run; ErrServerClosed after Shutdown is success.
	serveErr := make(chan error, 2)
	serve := func(ln net.Listener) {
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}
	if s.listen != nil {
		s.log.Info("daemon listening", "addr", s.Addr().String(), "pid", info.PID)
		go serve(s.listen)
	} else {
		s.log.Info("daemon loopback TCP closed; serving the socket only", "origin", s.Addr().String(), "pid", info.PID)
	}
	if s.sock != nil {
		// Shutdown closes this listener too, which unlinks the socket.
		s.log.Info("daemon socket listening", "path", s.sockPath)
		go serve(s.sock)
	}
	if onReady != nil {
		onReady()
	}

	select {
	case err := <-serveErr:
		// Serve died on its own (bind already happened, so this is a real
		// runtime failure) before any shutdown signal.
		return err
	case <-s.shutdownRequested:
		s.log.Info("shutdown requested over HTTP", "timeout", s.cfg.ShutdownTimeout)
	case <-ctx.Done():
		s.log.Info("shutdown signal received, draining connections", "timeout", s.cfg.ShutdownTimeout)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	stopClosing := make(chan struct{})
	go s.newConns.closeWhile(stopClosing)
	err := s.http.Shutdown(shutdownCtx)
	close(stopClosing)
	if err != nil {
		// The deadline elapsed with connections still open; force them closed.
		s.log.Warn("graceful shutdown timed out, forcing close", "err", err)
		_ = s.http.Close()
		return fmt.Errorf("graceful shutdown exceeded %s: %w", s.cfg.ShutdownTimeout, err)
	}

	s.log.Info("daemon stopped cleanly")
	return <-serveErr
}

func (s *Server) boundPort() int {
	if tcp, ok := s.Addr().(*net.TCPAddr); ok {
		return tcp.Port
	}
	return s.cfg.Port
}

func (s *Server) requestShutdown() {
	s.shutdownOnce.Do(func() {
		close(s.shutdownRequested)
	})
}

// RequestShutdown triggers the same clean shutdown as POST /shutdown: it makes
// Run return so the daemon exits without tearing down sessions. Idempotent.
func (s *Server) RequestShutdown() { s.requestShutdown() }

// StopAgentsOnExit reports whether a shutdown request asked the daemon to stop
// every agent's processes before it exits (a deliberate quit or logout),
// rather than leave them for the next daemon to adopt (a restart).
func (s *Server) StopAgentsOnExit() bool { return s.stopAgents.Load() }

// tokenRepairer restores the token file when it was deleted while the daemon
// runs, keeping the daemon's in-memory token so live clients stay valid. A
// rejected request is the only trigger, and a present file is left alone.
func tokenRepairer(path, token string, log *slog.Logger) func() {
	var mu sync.Mutex
	return func() {
		mu.Lock()
		defer mu.Unlock()
		if _, err := daemontoken.Read(path); !errors.Is(err, daemontoken.ErrMissing) {
			return
		}
		if err := daemontoken.Restore(path, token); err != nil {
			log.Warn("daemon token file missing and could not be restored", "path", path, "err", err)
			return
		}
		log.Warn("daemon token file was missing; restored it", "path", path)
	}
}
