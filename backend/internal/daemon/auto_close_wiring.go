package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/service/turnnotify"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

// autoCloseHooks backs the lifecycle's end-on-merge close (B407): it reports
// whether a message is still queued for the worker, and tells the worker's
// orchestrator (lane first, then the project's) that it was closed.
type autoCloseHooks struct {
	store        *sqlite.Store
	orchestrator func(ctx context.Context, project domain.ProjectID, lane string) (domain.SessionID, bool)
	send         func(ctx context.Context, to domain.SessionID, msg string) error
	log          *slog.Logger
	jobs         *turnnotify.ProcessJobs
	// branchWork reads a worker's git state for the finished-worker close (B588).
	branchWork func(ctx context.Context, rec domain.SessionRecord) domain.WorkerBranchWork
	// turns reads a closing worker's undelivered turn result (B557).
	turns interface {
		UndeliveredTurnResult(ctx context.Context, rec domain.SessionRecord) string
	}
}

// UndeliveredTurnResult reads the result a close would lose, or "" (B557).
func (h autoCloseHooks) UndeliveredTurnResult(ctx context.Context, rec domain.SessionRecord) string {
	if h.turns == nil {
		return ""
	}
	return h.turns.UndeliveredTurnResult(ctx, rec)
}

// withUndeliveredResult appends the result only this notice can still carry.
func withUndeliveredResult(notice, undelivered string) string {
	if undelivered == "" {
		return notice
	}
	return notice + "\nIts last turn result had not reached you:\n" + undelivered
}

func (h autoCloseHooks) HasQueuedMessages(ctx context.Context, id domain.SessionID) (bool, error) {
	conv, err := h.store.ConversationForSession(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Queued, promoted-but-not-started and still-running turns all mean a
	// request the worker has not answered (B494); NextQueuedTurn alone misses
	// the last two.
	return h.store.HasOpenTurns(ctx, conv.ID)
}

// OpenBacklogItems counts live backlog items linked to the worker: work it
// still owns after its first PR merged (B487).
func (h autoCloseHooks) OpenBacklogItems(ctx context.Context, id domain.SessionID) (int, error) {
	items, err := h.store.ListLiveBacklogItemsLinkedTo(ctx, domain.BacklogLinkSession, string(id))
	return len(items), err
}

// EndOnMergeSessions lists the live sessions marked end-on-merge.
func (h autoCloseHooks) EndOnMergeSessions(ctx context.Context) ([]domain.SessionID, error) {
	recs, err := h.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	var ids []domain.SessionID
	for _, rec := range recs {
		if rec.TerminateOnPRMerge && !rec.IsTerminated {
			ids = append(ids, rec.ID)
		}
	}
	return ids, nil
}

// NotifyHeld tells the orchestrator a close has been held past the bound.
func (h autoCloseHooks) NotifyHeld(ctx context.Context, rec domain.SessionRecord, reason string) {
	to, ok := h.orchestratorFor(ctx, rec)
	if !ok {
		return
	}
	msg := fmt.Sprintf("[auto] session_close_held: %s is idle with all PRs merged but has stayed open for hours (%s). Auto-close stopped retrying; close or restore it by hand.", rec.ID, reason)
	if err := h.send(ctx, to, msg); err != nil && h.log != nil {
		h.log.Warn("auto-close held notice failed", "session", rec.ID, "orchestrator", to, "err", err)
	}
}

// RunningJobs counts the background jobs (build, test run, launch) the worker
// is idle-waiting on; closing it would kill them and wipe its temp dir (B494).
// A session with no process roots (host descriptor missing or dead) reports
// none, which is logged so a close that skipped the check is diagnosable.
func (h autoCloseHooks) RunningJobs(ctx context.Context, rec domain.SessionRecord) (int, error) {
	if h.jobs == nil {
		return 0, nil
	}
	if h.jobs.Roots != nil && len(h.jobs.Roots(ctx, rec)) == 0 && h.log != nil {
		h.log.Info("auto-close job check: no process roots (host descriptor missing)", "session", rec.ID)
	}
	return h.jobs.RunningJobs(ctx, rec)
}

// orchestratorFor picks the worker's orchestrator, lane first.
func (h autoCloseHooks) orchestratorFor(ctx context.Context, rec domain.SessionRecord) (domain.SessionID, bool) {
	lane := ""
	if lanes, err := h.store.ListSessionLanes(ctx, rec.ProjectID); err == nil {
		lane = lanes[rec.ID]
	}
	return h.orchestrator(ctx, rec.ProjectID, lane)
}

func (h autoCloseHooks) NotifyClosed(ctx context.Context, rec domain.SessionRecord, requester, mergedBy domain.SessionID, undelivered string) {
	to, ok := h.orchestratorFor(ctx, rec)
	// The orchestrator that landed the PR already knows it ended (B560), but
	// not what the worker last said (B557).
	if !ok || (to == requester && undelivered == "") {
		return
	}
	// Only the session id: the display name is worker-controlled text.
	msg := fmt.Sprintf("[auto] session_closed: %s ended on its own because all its PRs are merged or closed and it was idle with nothing queued. Restore it if you still need it.", rec.ID)
	if to == requester || to == mergedBy {
		// The merge this orchestrator ran (possibly `pr land --kill-worker`,
		// whose kill then finds the row already ended) left the worker idle
		// with every PR merged, so the close followed its own action (B609).
		msg = fmt.Sprintf("[auto] session_closed: %s was closed right after the PR you merged: all its PRs are merged or closed and it was idle with nothing queued, so your kill, if any, found it already ended. Restore it if you still need it.", rec.ID)
	}
	msg = withUndeliveredResult(msg, undelivered)
	if err := h.send(ctx, to, msg); err != nil && h.log != nil {
		h.log.Warn("auto-close notice failed", "session", rec.ID, "orchestrator", to, "err", err)
	}
}
