package terminal

import (
	"regexp"
	"sync"
	"time"
)

// lockInputFence takes terminalID's write fence and returns its unlock.
func (m *Manager) lockInputFence(terminalID string) (unlock func()) {
	m.fencesMu.Lock()
	f := m.fences[terminalID]
	if f == nil {
		f = &inputFence{}
		m.fences[terminalID] = f
	}
	f.refs++
	m.fencesMu.Unlock()
	f.mu.Lock()
	return func() {
		f.mu.Unlock()
		m.fencesMu.Lock()
		f.refs--
		if f.refs == 0 {
			delete(m.fences, terminalID)
		}
		m.fencesMu.Unlock()
	}
}

// BeginInputDrain blocks new user input for one terminal while Session Manager
// hands its controller to another interface. Once it returns, every write that
// began before it has finished (it waits on the terminal's fence) and every later
// write is refused until the matching release runs. The release is idempotent.
func (m *Manager) BeginInputDrain(terminalID string) (lastInputAt time.Time, release func()) {
	unlock := m.lockInputFence(terminalID)
	m.inputMu.Lock()
	m.inputBlocked[terminalID]++
	lastInputAt = m.lastInputAt[terminalID]
	m.inputMu.Unlock()
	unlock()

	var once sync.Once
	return lastInputAt, func() {
		once.Do(func() {
			m.inputMu.Lock()
			defer m.inputMu.Unlock()
			if m.inputBlocked[terminalID] <= 1 {
				delete(m.inputBlocked, terminalID)
				return
			}
			m.inputBlocked[terminalID]--
		})
	}
}

// LastInputAt returns when the newest keystroke was accepted for one terminal,
// without blocking input. A drain (BeginInputDrain) drops keystrokes while it is
// held, so read-only callers must use this instead.
func (m *Manager) LastInputAt(terminalID string) time.Time {
	m.inputMu.Lock()
	defer m.inputMu.Unlock()
	return m.lastInputAt[terminalID]
}

// HoldInput takes one terminal's write fence and returns a release. Unlike
// BeginInputDrain, it does NOT set inputBlocked, so a concurrent keystroke is not
// dropped: its writeInput blocks on the fence until release and then writes, and
// input buffered during an attach waits the same way. Other terminals are not
// fenced. Session Manager holds it across the composer-draft confirmation and the
// pane write so a human keystroke cannot land between the final empty-frame proof
// and the paste (#5711). Hold it only briefly; this terminal's keystrokes queue
// for its duration.
//
// lastInputAt is read under the fence, so no keystroke accepted after it can
// reach the pane until release; one accepted just before may still be rendering,
// which is why the caller settles on it before trusting the screen. The release
// is idempotent.
func (m *Manager) HoldInput(terminalID string) (lastInputAt time.Time, release func()) {
	unlock := m.lockInputFence(terminalID)
	m.inputMu.Lock()
	lastInputAt = m.lastInputAt[terminalID]
	m.inputMu.Unlock()
	var once sync.Once
	return lastInputAt, func() { once.Do(unlock) }
}

// TerminalOnScreen reports whether a client is showing the terminal. A client
// without a grid keeps it parked off screen: it attached without one, or gave
// it up with a 0x0 resize when its pane or window was hidden. (Same signal as
// Account Manager's session moves, #6274.)
func (m *Manager) TerminalOnScreen(terminalID string) bool {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[terminalID]
	if s == nil {
		return false
	}
	cols, rows := largestGrid(s.members)
	return cols > 0 && rows > 0
}

// inputRefused reports whether a drain refuses input for terminalID right now.
func (m *Manager) inputRefused(terminalID string) bool {
	m.inputMu.Lock()
	defer m.inputMu.Unlock()
	return m.inputBlocked[terminalID] > 0
}

func (m *Manager) writeInput(terminalID string, a *attachment, raw []byte, release func()) {
	unlock := m.lockInputFence(terminalID)
	defer unlock()
	m.inputMu.Lock()
	blocked := m.inputBlocked[terminalID] > 0
	m.inputMu.Unlock()
	if blocked {
		release()
		return
	}
	_ = a.writeLeased(raw, release)
	m.recordInputAt(terminalID, raw)
}

// recordInputAt starts the render settle window after input acceptance and
// refreshes it after buffered replay. Callers hold the terminal's write fence.
func (m *Manager) recordInputAt(terminalID string, raw []byte) {
	if terminalReports.Match(raw) {
		return
	}
	m.inputMu.Lock()
	m.lastInputAt[terminalID] = time.Now()
	m.inputMu.Unlock()
}

// terminalReports matches bytes the terminal view sends on its own, not typed by
// anyone: focus in and out, and replies to the agent's queries about the
// device and cursor, colours, modes ($y), keyboard flags (?…u), window size (…t)
// and version (a DCS string). They are no reason to think the agent was given
// work. A key under the kitty keyboard protocol ends in u too, but never has ?.
var terminalReports = regexp.MustCompile(`^(?:\x1b\[[IO]|\x1b\[[?>]?[0-9;]*[cRn]|\x1b\[\??[0-9;]*\$y|\x1b\[\?[0-9]*u|\x1b\[[0-9;]+t|\x1b\][0-9]+;[^\x07\x1b]*(?:\x07|\x1b\\)|\x1bP[^\x1b]*\x1b\\)+$`)
