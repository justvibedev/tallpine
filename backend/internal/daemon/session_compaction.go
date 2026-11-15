package daemon

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	sessionmanager "tallpine.eu/tallpine/backend/internal/session_manager"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// compactionViewer is the session manager's auto-compaction readout.
type compactionViewer interface {
	AutoCompactView(context.Context, domain.SessionRecord) (domain.AutoCompactView, bool)
}

// The session manager and the review engine find the baseline reader on the
// store they are given, and the HTTP readout finds the viewer on the session
// manager, by type assertion: these keep both in place.
var (
	_ ports.AutoCompactBaselineReader = (*sqlite.Store)(nil)
	_ compactionViewer                = (*sessionmanager.Manager)(nil)
)

// sessionCompaction is a session's auto-compaction readout for session get and
// spawn: a running chat reports its controller's launch values, any other
// session what its launch path resolves (sessionmanager.Manager.AutoCompactView).
func (b *boot) sessionCompaction(ctx context.Context, sess domain.Session) (domain.AutoCompactView, bool) {
	if b.chatSvc != nil {
		if view, ok := b.chatSvc.AutoCompactView(ctx, sess.ID); ok {
			return view, true
		}
	}
	manager, ok := b.sessMgr.(compactionViewer)
	if !ok {
		return domain.AutoCompactView{}, false
	}
	return manager.AutoCompactView(ctx, sess.SessionRecord)
}
