package daemon

import (
	"context"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/daemontoken"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessiontoken"
)

type liveSessionLister interface {
	ListLiveSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// rotateIdleSessionKey rotates the scoped-token HMAC key when no session is
// live, so a stolen key stops minting valid tokens at the next restart. With
// any live session the key stays: running agents hold tokens derived from it.
// Any failure keeps the current authority, which is always safe.
func rotateIdleSessionKey(ctx context.Context, tokenPath string, store liveSessionLister, current *sessiontoken.Authority, log *slog.Logger) *sessiontoken.Authority {
	live, err := store.ListLiveSessions(ctx)
	if err != nil {
		log.Warn("session key rotation skipped: cannot list live sessions", "error", err)
		return current
	}
	if len(live) > 0 {
		return current
	}
	key, err := daemontoken.RotateSessionKey(tokenPath)
	if err != nil {
		log.Warn("session key rotation failed; keeping the current key", "error", err)
		return current
	}
	log.Info("rotated session key: no live sessions")
	return sessiontoken.New(key)
}
