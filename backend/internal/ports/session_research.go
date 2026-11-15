package ports

import (
	"context"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// SessionResearchStore persists an orchestrator's research mode (B736) so a
// daemon restart restores it: the window, when it was switched on, and the
// last turn the quiet-hour expiry runs from.
type SessionResearchStore interface {
	SessionResearch(ctx context.Context, id domain.SessionID) (domain.SessionResearch, bool, error)
	// SetSessionResearch persists research mode; nil clears it.
	SetSessionResearch(ctx context.Context, id domain.SessionID, research *domain.SessionResearch) error
	// TouchSessionResearch records a finished turn while research mode is on.
	TouchSessionResearch(ctx context.Context, id domain.SessionID, at time.Time) error
	// ClearAllSessionResearch ends every persisted research mode.
	ClearAllSessionResearch(ctx context.Context) error
}
