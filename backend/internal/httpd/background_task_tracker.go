package httpd

import (
	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// BackgroundTaskTracker is the slice of the background-task tracker the API
// reads and updates.
type BackgroundTaskTracker interface {
	Waiting(domain.SessionID) []string
	Running(domain.SessionID) []bgtasks.Info
	ExpectStop(domain.SessionID, string)
	CancelStop(domain.SessionID, string)
}
