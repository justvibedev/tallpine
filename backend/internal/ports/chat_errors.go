package ports

import "errors"

// Storage-level conversation queue refusals. They live here, not in the sqlite
// store, so the HTTP controllers and chat service can match them without
// importing storage; the store wraps these same values.
var (
	// ErrQueuedTurnNotAvailable means the selected turn cannot be reserved: it is
	// no longer queued, or was already promoted or removed.
	ErrQueuedTurnNotAvailable = errors.New("queued turn is not available for promotion")
	// ErrQueuedEditDeliveryConflict refuses reuse of a key for a different mutation.
	ErrQueuedEditDeliveryConflict = errors.New("queued edit delivery key reused")
)
