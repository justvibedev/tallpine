package daemon

import (
	"context"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/bgtasks"
	"tallpine.eu/tallpine/backend/internal/domain"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/service/orchestratorcompact"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// idleCompactionTick paces the idle compaction sweep; the idle wait itself is
// domain.IdleCompactWait.
const idleCompactionTick = time.Minute

// startIdleCompaction compacts idle orchestrators between tasks (B204) and
// hooks the same check in front of Chat hibernation, so an orchestrator that
// is about to sleep wakes up small.
func startIdleCompaction(ctx context.Context, store *sqlite.Store, chat *chatsvc.Service, tasks *bgtasks.Tracker,
	handoff func(context.Context, domain.SessionID) error,
	hibernation interface {
		SetBeforeHibernate(func(context.Context, domain.SessionID) (bool, error))
	}, log *slog.Logger,
) {
	sweeper := orchestratorcompact.New(orchestratorcompact.Deps{
		Store: store,
		State: func(ctx context.Context, id domain.SessionID) (orchestratorcompact.ChatState, bool) {
			st, ok := chat.IdleCompaction(ctx, id)
			return orchestratorcompact.ChatState{Busy: st.Busy, ContextUsed: st.ContextUsed, Threshold: st.Threshold, Handoff: st.Handoff, Research: st.Research}, ok
		},
		Compact: func(ctx context.Context, id domain.SessionID) error {
			_, err := chat.CompactIdle(ctx, id)
			return err
		},
		ClearResearch:   chat.ClearResearch,
		Handoff:         handoff,
		BackgroundTasks: func(id domain.SessionID) int { return len(tasks.Running(id)) },
		Log:             log,
	})
	hibernation.SetBeforeHibernate(sweeper.MaybeCompact)
	go func() {
		ticker := time.NewTicker(idleCompactionTick)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := sweeper.Sweep(ctx); err != nil && ctx.Err() == nil {
					log.Warn("idle orchestrator compaction failed", "err", err)
				}
			}
		}
	}()
}
