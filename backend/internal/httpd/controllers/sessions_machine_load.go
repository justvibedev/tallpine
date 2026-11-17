package controllers

import (
	"context"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/service/machineload"
)

// MachineLoadChecker reads the machine against the reviewer load limits.
type MachineLoadChecker interface {
	Check(ctx context.Context) (machineload.Status, error)
}

// machineBusyWarning adds the busy-machine notice to a spawn's warnings. The
// spawn is never held: a worker still starts, and its caller learns the machine
// is loaded. A gate that cannot read the machine adds nothing.
func (c *SessionsController) machineBusyWarning(ctx context.Context, warnings []string) []string {
	if c.MachineLoad == nil {
		return warnings
	}
	status, err := c.MachineLoad.Check(ctx)
	if err != nil {
		slog.Default().WarnContext(ctx, "spawn could not read the machine load; no busy warning", "err", err)
		return warnings
	}
	if !status.Busy {
		return warnings
	}
	return append(warnings, status.Reason())
}
