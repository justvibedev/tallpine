package sessionmanager

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SetBeforeHibernate installs a check idle hibernation runs first; a true
// result keeps the session awake this pass (idle orchestrator compaction,
// B204). Set once at wiring, before the hibernation sweep starts.
func (m *Manager) SetBeforeHibernate(before func(context.Context, domain.SessionID) (bool, error)) {
	m.beforeHibernate = before
}
