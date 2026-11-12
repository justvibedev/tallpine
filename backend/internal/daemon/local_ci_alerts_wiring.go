package daemon

import (
	"context"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/localcialerts"
)

// localCIAlertSessions is what the local-CI alert reader needs from the store.
type localCIAlertSessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// wireLocalCIAlerts starts the reader that passes the launchd local-CI loop's
// alerts (build done or failed, main red or green) to the main-health
// orchestrator (B680). The loop cannot call the daemon under the peer check;
// the daemon reads its alerts file instead. An isolated daemon
// (TALLPINE_DATA_DIR) has its own state root and so no local-CI directory;
// TALLPINE_LOCAL_CI_DIR is not read, so a test daemon under the loop never
// takes the real alerts.
func (b *boot) wireLocalCIAlerts() {
	reader := &localcialerts.Reader{
		Dir:    filepath.Join(b.cfg.StateDir, "local-ci"),
		Target: localCIAlertTarget(b.store, orchestratorPicker(b.store)),
		Send: func(ctx context.Context, to domain.SessionID, msg string) error {
			return b.sessionSvc.Send(ctx, to, msg, nil)
		},
		Log: b.log,
	}
	done := reader.Start(b.ctx)
	b.cleanup(func() {
		b.stop()
		<-done
	})
}

// localCIAlertTarget resolves the session named in notify-target: itself while
// it is a live orchestrator, else its project's main orchestrator, so a
// replaced owner still hears. An unknown id has no target.
func localCIAlertTarget(store localCIAlertSessions, pick func(context.Context, domain.ProjectID, string) (domain.SessionID, bool)) func(context.Context, domain.SessionID) (domain.SessionID, bool) {
	return func(ctx context.Context, id domain.SessionID) (domain.SessionID, bool) {
		rec, ok, err := store.GetSession(ctx, id)
		if err != nil || !ok {
			return "", false
		}
		if rec.Kind == domain.KindOrchestrator && !rec.IsTerminated && rec.Activity.State != domain.ActivityExited {
			return rec.ID, true
		}
		if pick == nil {
			return "", false
		}
		return pick(ctx, rec.ProjectID, "")
	}
}
