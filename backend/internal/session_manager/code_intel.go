package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// persistCodeIntel saves a code intelligence spawn's flag with the session's
// launch options, so a restart or provider-interface switch relaunches it the
// same way (prompt line and grep deny list). Best effort: the first launch
// already carries the mode.
func (m *Manager) persistCodeIntel(ctx context.Context, st *spawnState) {
	if !st.cfg.CodeIntel {
		return
	}
	store, ok := m.store.(launchOptionsStore)
	if !ok {
		return
	}
	if err := store.SetSessionLaunchOptions(ctx, st.id, domain.AgentLaunchOptions{CodeIntel: true}); err != nil {
		m.logger.Warn("persist code-intel launch option", "session", st.id, "error", err)
	}
}
