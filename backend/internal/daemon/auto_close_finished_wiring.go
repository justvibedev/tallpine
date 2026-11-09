package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/workspace/gitworktree"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// FinishedWorkerKeepReason gathers the facts for closing a worker that has no
// merged PR (B588). Reports come first because they settle most workers without
// touching git; any error keeps the worker, by way of the caller's err path.
func (h autoCloseHooks) FinishedWorkerKeepReason(ctx context.Context, rec domain.SessionRecord, now time.Time) (string, error) {
	project, ok, err := h.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return "", err
	}
	// The per-session flag is copied at spawn; a project switched off since
	// must still stop this path for the workers it already has.
	if ok && project.Config.TerminateOnPRMerge != nil && !*project.Config.TerminateOnPRMerge {
		return "project auto-close is off", nil
	}
	reports, err := h.store.ListReportsBySession(ctx, rec.ID)
	if err != nil {
		return "", err
	}
	prs, err := h.store.ListPRsBySession(ctx, rec.ID)
	if err != nil {
		return "", err
	}
	if reason := domain.FinishedWorkerKeepReason(domain.FinishedWorkerFacts{Session: rec, Reports: reports, PRs: prs, Now: now}); reason != "" {
		return reason, nil
	}
	if h.branchWork == nil {
		return "branch state unknown: no probe wired", nil
	}
	if reason := h.branchWork(ctx, rec).KeepReason(); reason != "" {
		return reason, nil
	}
	if reason := worktreeDirtyReason(ctx, rec.Metadata.WorkspacePath); reason != "" {
		return reason, nil
	}
	runs, err := h.store.ListRunningReviewRunsBySession(ctx, rec.ID)
	if err != nil {
		return "", err
	}
	if len(runs) > 0 {
		return "a review is running", nil
	}
	return "", nil
}

// worktreeDirtyReason applies the worktree teardown's own dirtiness rule; a
// status that cannot be read keeps the worker.
func worktreeDirtyReason(ctx context.Context, path string) string {
	out, err := ports.DefaultGit().Output(ctx, path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "worktree status unreadable"
	}
	for _, line := range strings.Split(out, "\n") {
		if gitworktree.StatusLineIsWork(path, line) {
			return "uncommitted changes in the worktree"
		}
	}
	return ""
}

// NotifyFinishedClosed tells the worker's orchestrator a PR-less worker ended.
func (h autoCloseHooks) NotifyFinishedClosed(ctx context.Context, rec domain.SessionRecord, undelivered string) {
	to, ok := h.orchestratorFor(ctx, rec)
	if !ok {
		return
	}
	// Only the session id: the display name is worker-controlled text.
	msg := fmt.Sprintf("[auto] session_closed: %s ended on its own because it finished with no open PR, no unmerged work and nothing queued. Restore it if you still need it.", rec.ID)
	msg = withUndeliveredResult(msg, undelivered)
	if err := h.send(ctx, to, msg); err != nil && h.log != nil {
		h.log.Warn("auto-close notice failed", "session", rec.ID, "orchestrator", to, "err", err)
	}
}
