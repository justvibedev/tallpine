package daemon

import (
	"context"
	"log/slog"

	automationobserver "tallpine.eu/tallpine/backend/internal/observe/automations"
	automationsvc "tallpine.eu/tallpine/backend/internal/service/automation"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// startAutomations repairs crash-interrupted durable state before launching the
// cadence-only observer. Reconciliation is best effort so one malformed legacy
// row cannot prevent daemon readiness.
func startAutomations(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, logger *slog.Logger) (*automationsvc.Service, <-chan struct{}) {
	service := automationsvc.New(automationsvc.Deps{Store: store, Spawner: sessions})
	if err := service.Reconcile(ctx); err != nil {
		logger.Warn("automation startup reconciliation completed with errors", "err", err)
	}
	observer := automationobserver.New(service, automationobserver.Config{Logger: logger})
	return service, observer.Start(ctx)
}
