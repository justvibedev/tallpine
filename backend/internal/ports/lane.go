package ports

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// LaneReader is the optional store capability behind orchestrator lanes. A
// store without it leaves every session un-laned, and callers type-assert for
// it. Writers and report routing extend it with their own methods.
type LaneReader interface {
	SessionLane(context.Context, domain.SessionID) (domain.SessionLane, error)
	ListSessionLanes(context.Context, domain.ProjectID) (map[domain.SessionID]string, error)
}
