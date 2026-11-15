package controllers

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// compactionView is the session's auto-compaction readout, ok false when the
// controller has no reader or the session has none (a terminated session, or
// an agent Tallpine sets no window on).
func (c *SessionsController) compactionView(ctx context.Context, sess domain.Session) (domain.AutoCompactView, bool) {
	if c.Compaction == nil || sess.IsTerminated {
		return domain.AutoCompactView{}, false
	}
	return c.Compaction(ctx, sess)
}
