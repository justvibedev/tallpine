package terminal

import (
	"context"
	"encoding/base64"
	"sync"

	"tallpine.eu/tallpine/backend/internal/cdc"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// connState is the per-connection mutable state.
type connState struct {
	mgr    *Manager
	conn   wsConn
	ctx    context.Context
	cancel context.CancelFunc
	out    *outQueue

	mu        sync.Mutex
	terms     map[string]*attachment // terminal id -> this conn's own attach PTY
	unsubEvts func()
	closed    bool

	// writers holds each terminal's newest writer. It is only touched by the
	// read loop and, once it has returned, by cleanup, so it needs no lock.
	writers map[string]*inputWriter
}

// inputQueueDepth bounds the keystroke messages one terminal can queue behind
// its held fence before the connection's read loop waits for room.
const inputQueueDepth = 256

// inputWriter writes one terminal's input from one connection in arrival order,
// off the connection's read loop. A keystroke waiting on a held fence
// (HoldInput) then stalls only that terminal, not the connection's other
// terminals, resizes, or pongs — the desktop app multiplexes every terminal
// over one connection. Closing the terminal retires its writer; it exits once
// its queue drains.
type inputWriter struct {
	queue chan queuedInput
	done  chan struct{}
	// retired means queue is closed. Read loop only.
	retired bool
}

// queuedInput is a keystroke message to write, or (closeAfter) the attachment
// to close once every keystroke queued before it is written.
type queuedInput struct {
	a          *attachment
	raw        []byte
	release    func()
	closeAfter bool
}

// run writes the queue in order. prev is the terminal's retired writer, if it
// may still be draining; waiting for it keeps keys typed before a close and
// reopen ahead of keys typed after.
func (w *inputWriter) run(m *Manager, terminalID string, prev *inputWriter) {
	defer close(w.done)
	if prev != nil {
		<-prev.done
	}
	for in := range w.queue {
		if in.closeAfter {
			in.a.close()
			continue
		}
		m.writeInput(terminalID, in.a, in.raw, in.release)
	}
}

// queueInput hands a keystroke message to the terminal's writer, starting one
// when it has none or its last one was retired.
func (c *connState) queueInput(terminalID string, in queuedInput) {
	w := c.writers[terminalID]
	if w == nil || w.retired {
		next := &inputWriter{queue: make(chan queuedInput, inputQueueDepth), done: make(chan struct{})}
		go next.run(c.mgr, terminalID, w)
		c.writers[terminalID] = next
		w = next
	}
	w.queue <- in
}

// closeAfterInput closes a once the keystrokes this connection queued for it
// are written, so a client that types and then switches away keeps its keys.
func (c *connState) closeAfterInput(terminalID string, a *attachment) {
	if w := c.writers[terminalID]; w == nil || w.retired {
		a.close()
		return
	}
	c.queueInput(terminalID, queuedInput{a: a, closeAfter: true})
}

// retireWriter closes terminalID's writer queue, so the writer exits once it
// has written what was queued, and forgets writers that already finished.
func (c *connState) retireWriter(terminalID string) {
	if w := c.writers[terminalID]; w != nil && !w.retired {
		close(w.queue)
		w.retired = true
	}
	for id, w := range c.writers {
		if !w.retired {
			continue
		}
		select {
		case <-w.done:
			delete(c.writers, id)
		default:
		}
	}
}

func (c *connState) handle(msg clientMsg) {
	switch msg.Ch {
	case chTerminal:
		c.handleTerminal(msg)
	case chSubscribe:
		c.handleSubscribe(msg)
	case chSystem:
		if msg.Type == msgPing {
			c.enqueue(serverMsg{Ch: chSystem, Type: msgPong})
		}
	}
}

func (c *connState) handleTerminal(msg clientMsg) {
	switch msg.Type {
	case msgOpen:
		c.openTerminal(msg.ID, msg.Rows, msg.Cols, msg.Role)
	case msgData:
		raw, err := base64.StdEncoding.DecodeString(msg.Data)
		if err != nil {
			return
		}
		release, ok := c.mgr.acquireSessionInput(msg.ID)
		if !ok {
			c.enqueue(serverMsg{Ch: chTerminal, ID: msg.ID, Type: msgError, Error: "session input is disabled while an exclusive session operation is in progress"})
			return
		}
		// A drain refuses input received while it is held, even if the write
		// itself would only run after the drain ends.
		a := c.lookup(msg.ID)
		if a == nil || c.mgr.inputRefused(msg.ID) {
			release()
			return
		}
		c.queueInput(msg.ID, queuedInput{a: a, raw: raw, release: release})
	case msgResize:
		// The client reports the grid it fits to; the manager arbitrates the shared
		// PTY's size across all viewers and resizes the attach Stream itself (see
		// reconcileLocked), so we do not resize this attachment directly here.
		c.mgr.updateTerminalSize(msg.ID, c, msg.Cols, msg.Rows, msg.Force)
	case msgClose:
		c.closeTerminal(msg.ID)
	}
}

// openTerminal opens this connection's own attach Stream for the pane. rows/cols
// are the client's grid from the open frame; the manager arbitrates the shared
// PTY size from it (see joinTerminal) and applies it to the Stream. role marks
// the client primary/secondary for that arbitration (empty = primary).
func (c *connState) openTerminal(id string, rows, cols uint16, role string) {
	if id == "" {
		c.enqueue(serverMsg{Ch: chTerminal, Type: msgError, Error: "missing terminal id"})
		return
	}
	c.mu.Lock()
	if _, ok := c.terms[id]; ok {
		c.mu.Unlock()
		return // already open on this conn; avoid a duplicate attach
	}
	c.mu.Unlock()

	// a is captured by onExit before assignment; safe because the attach loop —
	// the only thing that fires onExit — starts after the registration below.
	var a *attachment
	a = newAttachment(id, ports.RuntimeHandle{ID: id}, c.mgr.src,
		func() {
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgOpened})
		},
		func(data []byte) {
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgData, raw: data})
		},
		func() {
			// Clear the connection's entry for this id before sending exited so
			// a client that reopens the moment it sees exited finds no stale
			// entry and is served instead of dropped by the open guard. Guard on
			// identity: that reopen may already have installed a fresh
			// attachment under the same id, which must not be evicted.
			c.mu.Lock()
			if c.terms[id] == a {
				delete(c.terms, id)
			}
			c.mu.Unlock()
			c.mgr.leaveTerminal(id, c)
			c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgExited})
		},
		c.mgr.log)
	a.lockInput = func() func() { return c.mgr.lockInputFence(id) }
	a.onInputReplay = func(raw []byte) { c.mgr.recordInputAt(id, raw) }
	if err := c.mgr.track(a); err != nil {
		c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgError, Error: err.Error()})
		return
	}
	c.mu.Lock()
	c.terms[id] = a
	c.mu.Unlock()

	// Register with the shared-terminal arbiter, which sizes the attach Stream to
	// the authoritative grid (the open frame's rows/cols become this client's
	// requested size). An empty role means primary — the size-driving client.
	c.mgr.joinTerminal(id, c, a, cols, rows, role != roleSecondary)

	go func() {
		a.run(c.mgr.ctx)
		c.mgr.forget(a)
	}()
}

func (c *connState) closeTerminal(id string) {
	c.mu.Lock()
	a := c.terms[id]
	delete(c.terms, id)
	c.mu.Unlock()
	c.mgr.leaveTerminal(id, c)
	if a != nil {
		c.closeAfterInput(id, a)
	}
	c.retireWriter(id)
}

func (c *connState) lookup(id string) *attachment {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.terms[id]
}

func (c *connState) handleSubscribe(msg clientMsg) {
	if msg.Type != msgSubscribe || c.mgr.events == nil {
		return
	}
	c.mu.Lock()
	if c.unsubEvts != nil {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	unsub := c.mgr.events.Subscribe(func(e cdc.Event) {
		c.enqueue(serverMsg{
			Ch:   chSessions,
			Type: msgSnapshot,
			Session: &sessionUpdate{
				Seq:       e.Seq,
				ProjectID: e.ProjectID,
				SessionID: e.SessionID,
				EventType: string(e.Type),
			},
		})
	})
	c.mu.Lock()
	c.unsubEvts = unsub
	c.mu.Unlock()
}

func (c *connState) cleanup() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	attachments := make([]*attachment, 0, len(c.terms))
	ids := make([]string, 0, len(c.terms))
	for id, a := range c.terms {
		attachments = append(attachments, a)
		ids = append(ids, id)
	}
	c.terms = map[string]*attachment{}
	unsubEvts := c.unsubEvts
	c.unsubEvts = nil
	c.mu.Unlock()

	// Drop this connection from every shared terminal so the grid follows the
	// clients that remain (a disconnecting large client must let the PTY shrink
	// back to the smaller ones still attached).
	for _, id := range ids {
		c.mgr.leaveTerminal(id, c)
	}
	for i, a := range attachments {
		c.closeAfterInput(ids[i], a)
	}
	// The read loop has returned, so nothing queues input any more. Each
	// writer finishes what was queued (keystrokes, then closes) and exits.
	for _, w := range c.writers {
		if !w.retired {
			close(w.queue)
		}
	}
	if unsubEvts != nil {
		unsubEvts()
	}
	_ = c.conn.Close("server: connection closed")
}
