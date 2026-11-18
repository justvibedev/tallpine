package daemon

import (
	"context"
	"errors"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
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
// (TALLPINE_DATA_DIR elsewhere) has its own state root and so no local-CI directory;
// TALLPINE_LOCAL_CI_DIR is not read, so a test daemon under the loop never
// takes the real alerts.
func (b *boot) wireLocalCIAlerts() {
	reader := &localcialerts.Reader{
		Dir:    localCIDir(b.cfg),
		Target: localCIAlertTarget(b.store, orchestratorPicker(b.store)),
		Send: localCIAlertSend(func(ctx context.Context, to domain.SessionID, msg string) error {
			return b.sessionSvc.Send(ctx, to, msg, nil)
		}),
		Log: b.log,
	}
	done := reader.Start(b.ctx)
	b.cleanup(func() {
		b.stop()
		<-done
	})
}

// localCIDir is where the launchd scripts write alerts: <state root>/local-ci.
// The app starts the daemon with TALLPINE_DATA_DIR=~/.tallpine/data, which
// makes StateDir that data dir, so the real root comes from Config.HardenRoot,
// the one place that recognises it (B688).
func localCIDir(cfg config.Config) string { return filepath.Join(cfg.HardenRoot(), "local-ci") }

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

// deliveryUnconfirmedCode is the API code session.Send returns when the text was
// written to the agent's terminal but no turn start was seen (a busy or queued
// orchestrator). The message is delivered then; reporting it as a failure made the
// reader keep its cursor and paste the same alert again on the next tick (B821).
const deliveryUnconfirmedCode = "SESSION_DELIVERY_UNCONFIRMED"

// localCIAlertSend treats an unconfirmed delivery as sent, so the cursor advances.
func localCIAlertSend(send func(context.Context, domain.SessionID, string) error) func(context.Context, domain.SessionID, string) error {
	return func(ctx context.Context, to domain.SessionID, msg string) error {
		err := send(ctx, to, msg)
		var api *apierr.Error
		if errors.As(err, &api) && api.Code == deliveryUnconfirmedCode {
			return nil
		}
		return err
	}
}
