package review

import (
	stdctx "context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/observe/scm"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/process"
)

// LocalBranch is the branch a no-PR session's local review and land act on:
// the branch its worktree has checked out when the session owns it under the
// PR attribution rules (for example a worker that committed on a self-made
// tp/<session>/<topic> sibling of its tp/<session>/root branch), else the
// recorded session branch. A detached HEAD, an unreadable worktree, or a
// branch outside the session's namespace keeps the recorded branch.
func LocalBranch(ctx stdctx.Context, worker domain.SessionRecord) string {
	recorded := strings.TrimSpace(worker.Metadata.Branch)
	ws := strings.TrimSpace(worker.Metadata.WorkspacePath)
	if ws == "" || recorded == "" {
		return recorded
	}
	out, err := process.CommandContext(ctx, "git", ports.GitInDir(ws, "symbolic-ref", "--quiet", "HEAD")...).Output()
	if err != nil {
		return recorded
	}
	head, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "refs/heads/")
	if !ok || head == "" || head == recorded || !scm.SessionOwnsBranch(worker, false, head) {
		return recorded
	}
	return head
}
