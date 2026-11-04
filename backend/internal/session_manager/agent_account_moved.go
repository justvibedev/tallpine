package sessionmanager

import "tallpine.eu/tallpine/backend/internal/domain"

// SetAccountMoved binds what hears that a session's move to another agent
// account committed, so readiness and model catalogs that follow the account in
// use are refreshed. Bound late: the listener is built after the manager.
func (m *Manager) SetAccountMoved(moved func(domain.AgentHarness)) {
	m.accountMovedMu.Lock()
	m.accountMoved = moved
	m.accountMovedMu.Unlock()
}

func (m *Manager) notifyAccountMoved(harness domain.AgentHarness) {
	m.accountMovedMu.RLock()
	moved := m.accountMoved
	m.accountMovedMu.RUnlock()
	if moved != nil {
		moved(harness)
	}
}
