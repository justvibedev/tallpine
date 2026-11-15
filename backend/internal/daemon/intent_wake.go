package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	backlogsvc "tallpine.eu/tallpine/backend/internal/service/backlog"
)

// intentWakeInterval paces the sweep for intents whose wake time has come. The
// wake time is stored, not held in a timer, so a daemon restart loses nothing:
// the first sweep after start delivers wakes that came due while it was down.
const intentWakeInterval = 30 * time.Second

// intentWakeStore is the intent and project state the sweep reads and claims.
type intentWakeStore interface {
	SelectDueIntentWakes(ctx context.Context, now time.Time) ([]domain.OrchestratorIntent, error)
	ClaimIntentWake(ctx context.Context, id string, at time.Time) (bool, error)
	ReleaseIntentWake(ctx context.Context, id string) error
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// intentWaker sends the orchestrator of a project one system message.
type intentWaker interface {
	send(ctx context.Context, projectID domain.ProjectID, target domain.SessionID, message string, byUser bool) error
}

// sweepIntentWakes delivers each due wake once. A project that has not turned
// intentWake on keeps its wakes pending, so switching it on later delivers
// them. The wake is claimed before it is sent, so two sweeps never send it
// twice; a failed send (no live orchestrator) releases the claim for the next
// sweep. A crash between the claim and the send loses that wake (at most once).
func sweepIntentWakes(ctx context.Context, store intentWakeStore, waker intentWaker, now time.Time, log *slog.Logger) {
	due, err := store.SelectDueIntentWakes(ctx, now)
	if err != nil {
		log.Warn("intent wake: list due wakes failed", "err", err)
		return
	}
	for _, in := range due {
		rec, ok, err := store.GetProject(ctx, string(in.ProjectID))
		if err != nil || !ok || !rec.Config.IntentWake {
			continue
		}
		claimed, err := store.ClaimIntentWake(ctx, in.ID, now)
		if err != nil || !claimed {
			continue
		}
		if err := waker.send(ctx, in.ProjectID, "", intentWakeMessage(in, now), false); err != nil {
			// No reachable orchestrator is expected and retried every sweep: keep it quiet.
			if errors.Is(err, backlogsvc.ErrNoOrchestrator) {
				log.Debug("intent wake: no orchestrator, will retry", "intent", in.ID, "project", in.ProjectID)
			} else {
				log.Warn("intent wake: delivery failed, will retry", "intent", in.ID, "project", in.ProjectID, "err", err)
			}
			if relErr := store.ReleaseIntentWake(context.WithoutCancel(ctx), in.ID); relErr != nil {
				log.Warn("intent wake: release failed", "intent", in.ID, "err", relErr)
			}
		}
	}
}

func intentWakeMessage(in domain.OrchestratorIntent, now time.Time) string {
	due := ""
	if in.WakeAt != nil {
		due = " (due " + in.WakeAt.UTC().Format("2006-01-02 15:04 MST")
		if late := now.Sub(*in.WakeAt).Round(time.Minute); late >= 5*time.Minute {
			due += ", " + late.String() + " ago"
		}
		due += ")"
	}
	return fmt.Sprintf("Intent wake%s: intent %s is still open: %q. Check it now; clear it with `tallpine orchestrator intent done %s` once it is resolved.", due, in.ID, in.Text, in.ID)
}

// runIntentWakeSweep runs the sweep once startup reconcile is done, then on a
// fixed interval until ctx ends.
func runIntentWakeSweep(ctx context.Context, ready <-chan struct{}, store intentWakeStore, waker intentWaker, log *slog.Logger) {
	select {
	case <-ready:
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(intentWakeInterval)
	defer ticker.Stop()
	for {
		sweepIntentWakes(ctx, store, waker, time.Now().UTC(), log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
