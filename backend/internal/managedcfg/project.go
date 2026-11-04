package managedcfg

import "tallpine.eu/tallpine/backend/internal/domain"

const projectPrefix = "project."

func (l *Locks) project() Overlay[domain.ProjectConfig] {
	return Overlay[domain.ProjectConfig]{Locks: l, Prefix: projectPrefix}
}

// ApplyProject sets every locked project.<field> that has a managed value into
// cfg, for every project. A value that does not fit cfg is ignored (the field
// stays locked at its stored value).
func (l *Locks) ApplyProject(cfg domain.ProjectConfig) domain.ProjectConfig {
	return l.project().Apply(cfg)
}

// LockedProjectChanges returns the locked project keys whose value differs
// between a and b.
func (l *Locks) LockedProjectChanges(a, b domain.ProjectConfig) []string {
	return l.project().Changed(a, b)
}

// RestoreLocked returns in with every locked project field taken from stored,
// so a write never persists a managed value as the user's own.
func (l *Locks) RestoreLocked(stored, in domain.ProjectConfig) domain.ProjectConfig {
	return l.project().Restore(stored, in)
}
