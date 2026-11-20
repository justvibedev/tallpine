package domain

// SessionRole marks a worker session with a special job. Empty is an ordinary
// worker. A planner is a read-only worker that explores and writes a plan; its
// done report completes the plan, not the backlog item it is linked to.
type SessionRole string

// SessionRolePlanner is the read-only planning worker (B205).
const SessionRolePlanner SessionRole = "planner"

// SessionRoleContextAnalyst is the short-lived daemon-spawned worker that drafts
// context health patches (B827). It never edits the repository.
const SessionRoleContextAnalyst SessionRole = "context_analyst"

// Valid reports whether r is empty or a known role.
func (r SessionRole) Valid() bool {
	return r == "" || r == SessionRolePlanner || r == SessionRoleContextAnalyst
}
