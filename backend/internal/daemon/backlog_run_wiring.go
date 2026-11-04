package daemon

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/config"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newBacklogRunService builds the run-mode service behind the TALLPINE_BACKLOG_RUN
// switch; with the switch off every call answers "off".
func newBacklogRunService(cfg config.Config, store *sqlite.Store, sessions backlogRunSessions, plans *backlogsvc.PlanService) *backlogsvc.RunService {
	return backlogsvc.NewRunService(backlogsvc.RunDeps{
		Store: store, Notify: newBacklogNotifier(sessions, store), Enabled: cfg.BacklogRun, Plans: plans,
	})
}

// newBacklogNotifier is the one constructor, so pick can never be left unset.
func newBacklogNotifier(sessions backlogRunSessions, store *sqlite.Store) backlogRunNotifier {
	return backlogRunNotifier{sessions: sessions, pick: orchestratorPicker(store)}
}

type backlogRunSessions interface {
	List(ctx context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error)
	SendWithOptions(ctx context.Context, id domain.SessionID, message string, attachment *ports.SpawnAttachment, options ports.MessageDeliveryOptions) error
}

// backlogRunNotifier delivers run-mode messages to the project's main
// orchestrator as the owner's own words: they only ever follow an owner click.
// Plan notices go to the orchestrator that started the plan and are system
// messages.
type backlogRunNotifier struct {
	sessions backlogRunSessions
	// pick is the project's default (main) orchestrator, the one routing rule
	// domain.PickOrchestrator.
	pick func(context.Context, domain.ProjectID, string) (domain.SessionID, bool)
}

func (n backlogRunNotifier) NotifyOrchestrator(ctx context.Context, projectID domain.ProjectID, message string) error {
	return n.send(ctx, projectID, "", message, true)
}

func (n backlogRunNotifier) NotifyPlanOrchestrator(ctx context.Context, projectID domain.ProjectID, target domain.SessionID, message string) error {
	return n.send(ctx, projectID, target, message, false)
}

func (n backlogRunNotifier) send(ctx context.Context, projectID domain.ProjectID, target domain.SessionID, message string, byUser bool) error {
	active := true
	found, err := n.sessions.List(ctx, sessionsvc.ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
	if err != nil {
		return err
	}
	to, ok := liveTarget(found, target)
	if !ok && n.pick != nil {
		to, ok = n.pick(ctx, projectID, "")
	}
	if !ok {
		return backlogsvc.ErrNoOrchestrator
	}
	return n.sessions.SendWithOptions(ctx, to, message, nil, ports.MessageDeliveryOptions{AuthoredByUser: byUser})
}

// liveTarget reports target when it is among the live orchestrators.
func liveTarget(found []domain.Session, target domain.SessionID) (domain.SessionID, bool) {
	for _, s := range found {
		if target != "" && s.ID == target {
			return s.ID, true
		}
	}
	return "", false
}
