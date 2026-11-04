package sessionmanager

import "tallpine.eu/tallpine/backend/internal/domain"

// agentOperationIs reports whether the given agent operation is in flight for a
// session.
func (m *Manager) agentOperationIs(id domain.SessionID, kind agentOperationKind) bool {
	m.agentOpMu.Lock()
	defer m.agentOpMu.Unlock()
	return m.agentOperationActiveLocked(id) && m.agentOperations[id] == kind
}
