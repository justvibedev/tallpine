package accountpool

import (
	"tallpine.eu/tallpine/backend/internal/agentpolicy"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// Outcome is the account pool setting in force. The zero Outcome is the
// default placement with failover off: nothing changes.
type Outcome struct {
	placement domain.AccountPlacement
	failover  bool
}

// Record implements agentpolicy.Rule's outcome contract; the values and their
// source are already in the decision record.
func (Outcome) Record() map[string]any { return nil }

// Of returns the account pool outcome of a decision; the zero Decision yields
// the defaults.
func Of(d agentpolicy.Decision) Outcome {
	if out, ok := d.Outcome(Name); ok {
		if typed, ok := out.(Outcome); ok {
			return typed
		}
	}
	return Outcome{}
}

// Placement is how a new session without a chosen account picks one.
func (o Outcome) Placement() domain.AccountPlacement {
	if o.placement == "" {
		return domain.AccountPlacementDefault
	}
	return o.placement
}

// Failover reports whether a usage-limited session moves to another account.
func (o Outcome) Failover() bool { return o.failover }
