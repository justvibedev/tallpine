package terminal

// joinTerminal registers a client (its connection + attach Stream + requested
// grid + role) as a viewer of terminal id, then reconciles the shared grid.
func (m *Manager) joinTerminal(id string, c *connState, att *attachment, cols, rows uint16, primary bool) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		s = &sharedTerm{members: map[*connState]*termMember{}}
		m.shared[id] = s
	}
	previousCols, previousRows := s.authCols, s.authRows
	s.members[c] = &termMember{att: att, cols: cols, rows: rows, primary: primary}
	m.reconcileLocked(id, s, false)
	// A follower joining a PTY that is already at its authoritative size wouldn't
	// be touched by reconciliation. Apply the grid only to its new attachment and
	// tell that client directly, without re-signalling every existing viewer.
	if s.authCols > 0 && s.authRows > 0 &&
		previousCols == s.authCols && previousRows == s.authRows {
		_ = att.resize(s.authRows, s.authCols)
		c.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgResize, Cols: s.authCols, Rows: s.authRows})
	}
}

// updateTerminalSize records a client's newly requested grid and reconciles.
// Identical normal updates are no-ops; force is reserved for an explicit
// recovery re-signal when an attach client may have missed the prior SIGWINCH.
func (m *Manager) updateTerminalSize(id string, c *connState, cols, rows uint16, force bool) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		return
	}
	mem := s.members[c]
	if mem == nil {
		return
	}
	if !force && mem.cols == cols && mem.rows == rows {
		return
	}
	mem.cols, mem.rows = cols, rows
	m.reconcileLocked(id, s, force)
}

// leaveTerminal drops a client from a shared terminal and reconciles so the grid
// follows the remaining clients (or the entry is dropped when the last leaves).
func (m *Manager) leaveTerminal(id string, c *connState) {
	m.sharedMu.Lock()
	defer m.sharedMu.Unlock()
	s := m.shared[id]
	if s == nil {
		return
	}
	if _, ok := s.members[c]; !ok {
		return
	}
	delete(s.members, c)
	if len(s.members) == 0 {
		delete(m.shared, id)
		return
	}
	m.reconcileLocked(id, s, false)
}

// reconcileLocked picks the authoritative grid for a shared terminal and applies
// it only when it changed (or explicit recovery forces a re-signal). This keeps
// a non-authoritative viewer's local resize from repainting the shared TUI at an
// unchanged grid. Caller holds m.sharedMu.
func (m *Manager) reconcileLocked(id string, s *sharedTerm, force bool) {
	cols, rows := largestGrid(s.members)
	if cols == 0 || rows == 0 {
		return // no client has reported a usable size yet
	}
	changed := cols != s.authCols || rows != s.authRows
	if !changed && !force {
		return
	}
	s.authCols, s.authRows = cols, rows
	for conn, mem := range s.members {
		_ = mem.att.resize(rows, cols)
		if changed {
			conn.enqueue(serverMsg{Ch: chTerminal, ID: id, Type: msgResize, Cols: cols, Rows: rows})
		}
	}
}

// largestGrid chooses the authoritative grid for a set of viewers: the largest
// (by area) among the PRIMARY clients if any primary has reported a size, else
// the largest among all. Choosing one client's cols AND rows as a pair (never an
// independent per-axis max) guarantees the grid matches a real client exactly, so
// that client renders pixel-correct and only smaller ones scale.
func largestGrid(members map[*connState]*termMember) (cols, rows uint16) {
	anyPrimary := false
	for _, mem := range members {
		if mem.primary && mem.cols > 0 && mem.rows > 0 {
			anyPrimary = true
			break
		}
	}
	bestArea := 0
	for _, mem := range members {
		if anyPrimary && !mem.primary {
			continue
		}
		if mem.cols == 0 || mem.rows == 0 {
			continue
		}
		if a := int(mem.cols) * int(mem.rows); a > bestArea {
			bestArea, cols, rows = a, mem.cols, mem.rows
		}
	}
	return cols, rows
}
