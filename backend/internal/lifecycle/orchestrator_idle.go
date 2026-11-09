package lifecycle

import "tallpine.eu/tallpine/backend/internal/domain"

// SetOrchestratorIdleObserver receives each turn end of a terminal orchestrator.
// Chat orchestrators signal this themselves from their controller, whose idle
// boundary also knows about compaction and handoffs. fn must not block.
func (m *Manager) SetOrchestratorIdleObserver(fn func(domain.SessionRecord)) {
	m.operationGateMu.Lock()
	m.orchestratorIdle = fn
	m.operationGateMu.Unlock()
}

func (m *Manager) notifyOrchestratorIdle(rec domain.SessionRecord) {
	if rec.Kind != domain.KindOrchestrator || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI {
		return
	}
	m.operationGateMu.RLock()
	fn := m.orchestratorIdle
	m.operationGateMu.RUnlock()
	if fn != nil {
		fn(rec)
	}
}
