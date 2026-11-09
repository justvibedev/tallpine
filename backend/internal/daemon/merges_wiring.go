package daemon

import (
	"context"

	"tallpine.eu/tallpine/backend/internal/process"

	"tallpine.eu/tallpine/backend/internal/domain"
	mergessvc "tallpine.eu/tallpine/backend/internal/service/merges"
	sessionsvc "tallpine.eu/tallpine/backend/internal/service/session"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// newMergesService builds the merge digest, labelling lanes by backlog ref.
func newMergesService(store *sqlite.Store) *mergessvc.Service {
	return mergessvc.New(mergessvc.Deps{
		Store: store,
		Contains: func(ctx context.Context, project domain.ProjectID, head, ancestor string) bool {
			rec, ok, err := store.GetProject(ctx, string(project))
			if err != nil || !ok || rec.Path == "" {
				return false
			}
			// Exit 0 = ancestor; a missing commit or any error keeps the warning.
			return process.CommandContext(ctx, "git", "-C", rec.Path, "merge-base", "--is-ancestor", ancestor, head).Run() == nil
		},
		LaneName: func(ctx context.Context, itemID string) string {
			item, ok, err := store.GetBacklogItem(ctx, itemID)
			if err != nil || !ok {
				return ""
			}
			return item.Ref()
		},
	})
}

// newMergeNotifier tells lane and main orchestrators about merges. A lane
// without its own orchestrator falls back to the project's main one.
func newMergeNotifier(store *sqlite.Store, sessions *sessionsvc.Service) *mergessvc.Notifier {
	return &mergessvc.Notifier{
		Service: newMergesService(store),
		// The one routing rule, domain.PickOrchestrator, as worker reports use it.
		Orchestrator: orchestratorPicker(store),
		Send: func(ctx context.Context, to domain.SessionID, msg string) error {
			return sessions.Send(ctx, to, msg, nil)
		},
	}
}

// orchestratorPicker routes to a project's live orchestrator for a lane.
func orchestratorPicker(store *sqlite.Store) func(context.Context, domain.ProjectID, string) (domain.SessionID, bool) {
	return func(ctx context.Context, project domain.ProjectID, lane string) (domain.SessionID, bool) {
		recs, err := store.ListSessions(ctx, project)
		if err != nil {
			return "", false
		}
		lanes, err := store.ListSessionLanes(ctx, project)
		if err != nil {
			return "", false
		}
		rec, ok := domain.PickOrchestrator(recs, lanes, lane)
		return rec.ID, ok
	}
}
