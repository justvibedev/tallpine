package lifecycle

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// liveOnlyTerminator is a terminator that skips an already-terminated session
// and reports false for it.
type liveOnlyTerminator interface {
	KillIfLive(ctx context.Context, id domain.SessionID) (bool, error)
}
