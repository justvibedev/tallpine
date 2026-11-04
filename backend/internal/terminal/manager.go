package terminal

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionguard"
)

// EventSource is the session-state feed the "sessions" channel forwards. The CDC
// broadcaster satisfies it; the interface lives next to its consumer so terminal
// does not depend on CDC internals beyond the Event shape.
type EventSource interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

// wsConn is the transport seam: a JSON-framed, single-reader/single-writer
// WebSocket connection. internal/httpd adapts coder/websocket to this; tests
// supply an in-memory fake. WriteJSON is only ever called from the per-conn
// writer goroutine; Ping may be called concurrently (it is a control frame).
type wsConn interface {
	ReadJSON(ctx context.Context, v any) error
	WriteJSON(ctx context.Context, v any) error
	Ping(ctx context.Context) error
	Close(reason string) error
}

const (
	defaultHeartbeat = 15 * time.Second
	// dataWatermark caps queued PTY output per connection. Above it the producing
	// attachment's read loop blocks, so tmux throttles at the source instead of
	// the connection being torn down under a flood.
	dataWatermark = 1 << 20
	// burstFlushInterval spaces writes while output streams. A frame queued
	// after a quiet period (a keystroke's echo) is written at once; frames
	// queued within this interval of the previous write wait for it, so a flood
	// leaves as a few large messages instead of thousands of tiny ones, each of
	// which costs the renderer an event, a JSON parse and a decode.
	burstFlushInterval = 4 * time.Millisecond
)

// Manager serves WebSocket clients, opening one attach Stream per opened pane
// per connection. There is no shared per-pane state to outlive a connection:
// the runtime owns the session (screen, scrollback, modes), and every fresh
// attach gets its full handshake + repaint. A client reconnect simply attaches
// again.
type Manager struct {
	src       Source
	events    EventSource
	log       *slog.Logger
	heartbeat time.Duration

	// ctx scopes every attachment's PTY lifetime; cancelled by Close.
	ctx    context.Context
	cancel context.CancelFunc

	mu          sync.Mutex
	attachments map[*attachment]struct{}
	closed      bool

	// sharedMu guards shared, the per-terminal-id view of every attached client.
	// It arbitrates the single PTY's grid across clients (see reconcileLocked).
	sharedMu sync.Mutex
	shared   map[string]*sharedTerm

	inputLeaseMu sync.RWMutex
	inputLease   sessionguard.InputLease

	// inputMu guards inputBlocked and lastInputAt. It is held only briefly, never
	// across a pane write.
	inputMu      sync.Mutex
	inputBlocked map[string]int
	lastInputAt  map[string]time.Time

	// fences holds one write fence per terminal id. Every pane write for that
	// terminal holds it — a keystroke, and input buffered during attach as it is
	// replayed — and so do BeginInputDrain and HoldInput, so they order against
	// those writes without stalling any other terminal's input. Lock order: a
	// fence, then inputMu.
	fencesMu sync.Mutex
	fences   map[string]*inputFence
}

// inputFence serializes one terminal's pane writes. refs counts holders and
// waiters so the entry is dropped once nobody needs it.
type inputFence struct {
	mu   sync.Mutex
	refs int
}

// sharedTerm tracks every client currently viewing one terminal id (one PTY) so
// the daemon can pick a single authoritative grid for it. A PTY has exactly one
// size; when several clients view it at once the largest eligible client wins and
// the rest render that grid scaled — this is what keeps a small phone from
// stripping down the desktop, and keeps every client's grid matched to the PTY so
// full-screen TUIs don't mis-render.
type sharedTerm struct {
	members            map[*connState]*termMember
	authCols, authRows uint16 // last authoritative grid broadcast/applied
}

// termMember is one client's contribution to a shared terminal: its own attach
// Stream, the grid it last asked for, and whether it drives the size (primary).
type termMember struct {
	att     *attachment
	cols    uint16
	rows    uint16
	primary bool
}

// Option configures a Manager.
type Option func(*Manager)

// WithHeartbeat overrides the ping interval.
func WithHeartbeat(d time.Duration) Option { return func(m *Manager) { m.heartbeat = d } }

// SetSessionInputLease late-binds the mutation-aware input authority. The
// terminal manager is created before the session manager during daemon boot.
func (m *Manager) SetSessionInputLease(lease sessionguard.InputLease) {
	m.inputLeaseMu.Lock()
	m.inputLease = lease
	m.inputLeaseMu.Unlock()
}

func (m *Manager) acquireSessionInput(id string) (func(), bool) {
	m.inputLeaseMu.RLock()
	lease := m.inputLease
	m.inputLeaseMu.RUnlock()
	if lease == nil {
		return func() {}, true
	}
	return lease.AcquireSessionInput(domain.SessionID(id))
}

// NewManager builds a Manager. src opens attach Streams; events feeds the session
// channel (may be nil to disable it). A nil logger falls back to slog.Default.
func NewManager(src Source, events EventSource, log *slog.Logger, opts ...Option) *Manager {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		src:          src,
		events:       events,
		log:          log,
		heartbeat:    defaultHeartbeat,
		ctx:          ctx,
		cancel:       cancel,
		attachments:  map[*attachment]struct{}{},
		shared:       map[string]*sharedTerm{},
		inputBlocked: map[string]int{},
		lastInputAt:  map[string]time.Time{},
		fences:       map[string]*inputFence{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Close tears down every live attachment and stops re-attach loops. Safe to
// call once on daemon shutdown.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	attachments := make([]*attachment, 0, len(m.attachments))
	for a := range m.attachments {
		attachments = append(attachments, a)
	}
	m.attachments = map[*attachment]struct{}{}
	m.mu.Unlock()

	m.cancel()
	for _, a := range attachments {
		a.close()
	}
}

// track registers a live attachment so Close can tear it down; it refuses new
// attachments once the manager is closed.
func (m *Manager) track(a *attachment) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return context.Canceled
	}
	m.attachments[a] = struct{}{}
	return nil
}

func (m *Manager) forget(a *attachment) {
	m.mu.Lock()
	delete(m.attachments, a)
	m.mu.Unlock()
}

// Serve runs the protocol loop for one client connection until it errors, the
// client disconnects, or ctx/the manager is cancelled. It owns the single writer
// goroutine and the heartbeat.
func (m *Manager) Serve(ctx context.Context, conn wsConn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	c := &connState{
		mgr:     m,
		conn:    conn,
		ctx:     ctx,
		cancel:  cancel,
		out:     newOutQueue(),
		terms:   map[string]*attachment{},
		writers: map[string]*inputWriter{},
	}
	defer c.cleanup()

	go c.writeLoop(ctx, burstFlushInterval)
	go c.heartbeatLoop(ctx, m.heartbeat)

	for {
		var msg clientMsg
		if err := conn.ReadJSON(ctx, &msg); err != nil {
			return
		}
		if ctx.Err() != nil {
			return
		}
		c.handle(msg)
	}
}
