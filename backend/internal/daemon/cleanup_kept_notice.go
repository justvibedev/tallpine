package daemon

import (
	"context"
	"log/slog"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	cleanupsvc "tallpine.eu/tallpine/backend/internal/service/cleanup"
)

// keptWorktreeNotifier turns a worktree cleanup kept into one worktree_kept
// notification per worktree and reason. The source key is stored with a unique
// index, so repeats on later passes (and after a restart) are no-ops. A nil
// owner sends nothing; a failure is logged and never fails the run.
func keptWorktreeNotifier(owner ownerNotifier) cleanupsvc.KeptNotifier {
	if owner == nil {
		return nil
	}
	return func(ctx context.Context, n cleanupsvc.KeptNotice) {
		body := n.Reason + "\nWorktree: " + n.Path
		if n.SalvagePath != "" {
			body += "\nSalvaged work: " + n.SalvagePath
		}
		err := owner.Notify(ctx, ports.NotificationIntent{
			Type: domain.NotificationWorktreeKept, SessionID: n.SessionID, ProjectID: n.ProjectID,
			SourceKey: "worktree-kept:" + string(n.SessionID) + ":" + n.ReasonKey,
			Title:     "Cleanup kept the worktree of " + string(n.SessionID),
			Body:      body,
		})
		if err != nil {
			slog.Warn("could not notify the owner of a kept worktree", "session", n.SessionID, "error", err)
		}
	}
}
