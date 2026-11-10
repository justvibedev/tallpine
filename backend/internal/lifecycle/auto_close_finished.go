package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// tryCloseFinishedWorker ends a worker that finished without a merged PR
// (review-only, plan, research, mock, or a host with no SCM adapter), under the
// same per-session flag as end-on-merge (B588). The caller has already refused
// active and needs-input sessions. Keeps do not arm a retry or notify anyone:
// most are permanent until a person acts, and the process sweep rechecks every
// flagged session, which also covers workers that predate this path.
func (m *Manager) tryCloseFinishedWorker(ctx context.Context, rec domain.SessionRecord) (held bool, err error) {
	hooks := m.autoCloseHooksOrNil()
	if hooks == nil {
		return false, nil
	}
	reason, err := m.finishedWorkerKeepReason(ctx, rec, hooks)
	if err != nil {
		return false, err
	}
	if reason != "" {
		// Not a hold: a keep must not start the end-on-merge hold clock.
		if m.autoCloseRetry.noteKept(rec.ID, reason) {
			slog.Info("finished-worker auto-close kept", "session", rec.ID, "reason", reason)
		}
		return false, nil
	}
	m.autoCloseRetry.forgetKept(rec.ID)
	slog.Info("auto-close firing", "session", rec.ID, "reason", "finished worker: done report, no open or unconfirmed PR, no unmerged work, idle past grace, nothing queued")
	undelivered := hooks.UndeliveredTurnResult(ctx, rec)
	killed, err := m.terminateCompletedSession(ctx, rec.ID)
	if err != nil || !killed {
		return false, err
	}
	// As with end-on-merge: a restore by hand must not be closed again.
	if err := m.mutate(ctx, rec.ID, func(cur domain.SessionRecord, _ time.Time) (domain.SessionRecord, bool) {
		cur.TerminateOnPRMerge = false
		return cur, true
	}); err != nil {
		slog.Warn("clear end-on-merge after finished-worker close failed", "session", rec.ID, "err", err)
	}
	hooks.NotifyFinishedClosed(ctx, rec, undelivered)
	return false, nil
}

// finishedWorkerKeepReason runs the slower report and git checks first so the
// queue and job checks are the freshest thing read before the kill.
func (m *Manager) finishedWorkerKeepReason(ctx context.Context, rec domain.SessionRecord, hooks autoCloseHooks) (string, error) {
	reason, err := hooks.FinishedWorkerKeepReason(ctx, rec, m.autoCloseRetry.now())
	if err != nil {
		return "", err
	}
	if reason != "" {
		// Fresh work or a pending report: the open-items clock measures only
		// continuous time held by open items.
		m.autoCloseRetry.finishedItems(rec.ID, false)
		return reason, nil
	}
	items, err := hooks.OpenBacklogItems(ctx, rec.ID)
	if err != nil {
		return "", fmt.Errorf("check backlog items for %s: %w", rec.ID, err)
	}
	// Bounded like the merged-PR hold (B608): items left open must not keep a
	// finished worker and its worktree forever.
	if items > 0 && !m.autoCloseRetry.finishedItems(rec.ID, true) {
		return "open linked backlog items", nil
	}
	if items == 0 {
		m.autoCloseRetry.finishedItems(rec.ID, false)
	}
	jobs, err := hooks.RunningJobs(ctx, rec)
	if err != nil {
		return "", fmt.Errorf("check background jobs for %s: %w", rec.ID, err)
	}
	if jobs > 0 {
		return "running background jobs", nil
	}
	queued, err := hooks.HasQueuedMessages(ctx, rec.ID)
	if err != nil {
		return "", fmt.Errorf("check queued messages for %s: %w", rec.ID, err)
	}
	if queued {
		return "unanswered input (queued message or open turn)", nil
	}
	return "", nil
}
