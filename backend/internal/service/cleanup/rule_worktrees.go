package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/pathx"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// refusalBackoff is how long a light pass leaves a worktree alone after its
// removal was refused (uncommitted changes, an open shell, a failing cleanup
// script). The daily full pass retries it regardless, so a project's
// pre-remove script never runs more than about once a day for a worktree
// that keeps being refused.
const refusalBackoff = 24 * time.Hour

// ignoredFilesNote goes on every worktree that is (or would be) removed: git
// does not count ignored files as changes, so they go with the worktree.
const ignoredFilesNote = "gitignored files inside it (such as .env) are deleted too"

// WorktreeOutcome is what the session manager did with one worktree.
type WorktreeOutcome struct {
	Removed     bool
	AlreadyGone bool
	// Kept is the user-facing reason the worktree was preserved.
	Kept string
}

// WorktreeCheck decides, under the session's teardown guard, whether its
// worktrees may go: it gets the freshly read record and every worktree path
// of the session. A non-empty reason keeps the session.
type WorktreeCheck func(ctx context.Context, rec domain.SessionRecord, worktrees []string) string

// WorktreeRemover reclaims one terminated session's workspace through the
// session manager, which takes the session's agent-operation guard, re-reads
// the session and runs check before anything is torn down, then re-checks
// under its workspace gate that no live session uses the workspace.
type WorktreeRemover interface {
	// WorktreePaths lists every worktree of the session: its workspace and,
	// for a multi-repo workspace project, each child repo's worktree.
	WorktreePaths(ctx context.Context, id domain.SessionID) ([]string, error)
	CleanupSession(ctx context.Context, id domain.SessionID, check WorktreeCheck) (WorktreeOutcome, error)
}

// WorktreeInspector returns why a worktree must be kept, or "" when removing
// it loses nothing. An error means the state is unknown, and the worktree is
// kept. requireMerged also keeps a worktree whose commits are only on its own
// branch.
type WorktreeInspector func(ctx context.Context, path string, requireMerged bool) (string, error)

// inProgressMarkers are git-dir entries that mean an operation was left
// half done: a rebase, merge, cherry-pick, revert or bisect.
var inProgressMarkers = []string{"rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}

func runGitIn(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := ports.DefaultGit().Plain(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(stderr.Bytes()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// GitInspect keeps a worktree that holds anything removal would lose or
// strand:
//
//   - uncommitted or untracked changes (`git status --porcelain`);
//   - a rebase, merge, cherry-pick, revert or bisect in progress;
//   - stash entries made on its branch;
//   - commits on HEAD that no other branch, remote branch or tag contains,
//     which covers commits made on a detached HEAD; with requireMerged that
//     includes commits that are only on the worktree's own branch.
func GitInspect(ctx context.Context, path string, requireMerged bool) (string, error) {
	// Without a repository at path, git would walk up and inspect a parent
	// repository instead (a dotfiles repo in ~). Refuse: the caller keeps it.
	top, err := runGitIn(ctx, path, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if !sameRealPath(top, path) {
		return "", fmt.Errorf("%s is not the top of a git repository (%s is)", path, top)
	}
	status, err := runGitIn(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "uncommitted changes", nil
	}
	gitDir, err := runGitIn(ctx, path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	for _, marker := range inProgressMarkers {
		if _, err := os.Lstat(filepath.Join(gitDir, marker)); err == nil {
			return "a rebase, merge or similar operation is in progress", nil
		}
	}
	branch, err := runGitIn(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	detached := branch == "HEAD"
	if !detached {
		stashes, err := runGitIn(ctx, path, "stash", "list", "--format=%gs")
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(stashes, "\n") {
			if strings.HasPrefix(line, "WIP on "+branch+":") || strings.HasPrefix(line, "On "+branch+":") {
				return "stashed changes on its branch", nil
			}
		}
	}
	// Commits HEAD has that nothing else keeps. The worktree's own branch
	// survives the worktree's removal, so it only counts as "elsewhere" when
	// merged work is not required.
	args := []string{"rev-list", "--max-count=1", "HEAD", "--not"}
	if requireMerged && !detached {
		args = append(args, "--exclude="+escapeGlob(branch))
	}
	args = append(args, "--branches", "--remotes", "--tags")
	unique, err := runGitIn(ctx, path, args...)
	if err != nil {
		return "", err
	}
	switch {
	case unique == "":
		return "", nil
	case detached:
		return "commits on a detached HEAD that no branch contains", nil
	default:
		return "commits that are not merged or pushed", nil
	}
}

// WorktreeRule removes the worktrees of terminated sessions once their
// retention has passed. A worktree is kept when GitInspect finds anything its
// removal would lose (uncommitted changes, an operation in progress, stashes,
// commits nothing else contains), when a live session uses the same path,
// when its status cannot be read, or when it lies outside the managed
// worktree root.
type WorktreeRule struct {
	root    string
	remover WorktreeRemover
	inspect WorktreeInspector

	salvage    *Salvager
	notifyKept KeptNotifier

	mu      sync.Mutex
	refused map[domain.SessionID]refusal
}

// SetSalvager makes the rule salvage a worktree's leftover work and remove it
// instead of keeping it (see Salvager), and expire old salvage.
func (r *WorktreeRule) SetSalvager(s *Salvager) { r.salvage = s }

// NewWorktreeRule builds the rule. root is the managed worktree root
// (<data dir>/worktrees); a nil inspect uses GitInspect.
func NewWorktreeRule(root string, remover WorktreeRemover, inspect WorktreeInspector) *WorktreeRule {
	if inspect == nil {
		inspect = GitInspect
	}
	return &WorktreeRule{root: root, remover: remover, inspect: inspect, refused: map[domain.SessionID]refusal{}}
}

// Category implements Rule.
func (*WorktreeRule) Category() Category { return CategoryWorktrees }

// Light implements Rule: leftover worktrees are retried hourly.
func (*WorktreeRule) Light() bool { return true }

// Enabled implements Rule.
func (*WorktreeRule) Enabled(p Policy) bool { return p.Worktrees.Enabled }

func (r *WorktreeRule) recentlyRefused(id domain.SessionID, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.refused[id]
	return ok && now.Sub(at.at) < refusalBackoff
}

// refusal is when a worktree was last kept and the reason, so a backed-off
// light pass can keep reporting an actionable reason (see noteKept).
type refusal struct {
	at     time.Time
	reason string
}

// keptReason is the reason recorded at the last refusal.
func (r *WorktreeRule) keptReason(id domain.SessionID) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refused[id].reason
}

func (r *WorktreeRule) noteRefusal(id domain.SessionID, now time.Time, reason string) {
	r.mu.Lock()
	r.refused[id] = refusal{at: now, reason: reason}
	r.mu.Unlock()
}

func (r *WorktreeRule) clearRefusal(id domain.SessionID) {
	r.mu.Lock()
	delete(r.refused, id)
	r.mu.Unlock()
}

// inspectAll runs the inspector on every worktree of a session and returns the
// first reason to keep it. An error, or a path outside the managed root, keeps
// it too: a multi-repo session goes only when every repo is safe.
func (r *WorktreeRule) inspectAll(ctx context.Context, paths []string, requireMerged bool, sv *salvageStep) string {
	return inspectWorktrees(ctx, r.root, r.inspect, paths, requireMerged, sv)
}

// SafetyCheck is the WorktreeCheck for a cleanup the user starts by hand
// (tallpine session cleanup): under the teardown guard it refuses a session that is
// live again and any worktree GitInspect would keep, in every repo of the
// session, without the scheduler's retention window or requireMerged (a
// session's branch survives the removal of its worktree). root is the managed
// worktree root; a nil inspect uses GitInspect.
func SafetyCheck(root string, inspect WorktreeInspector) WorktreeCheck {
	return SalvagingSafetyCheck(root, inspect, nil)
}

// SalvagingSafetyCheck is SafetyCheck that, with a salvager, saves a dirty
// worktree's work and lets it go instead of refusing it, as the scheduled
// rule does.
func SalvagingSafetyCheck(root string, inspect WorktreeInspector, salvage *Salvager) WorktreeCheck {
	if inspect == nil {
		inspect = GitInspect
	}
	return func(ctx context.Context, rec domain.SessionRecord, worktrees []string) string {
		if !rec.IsTerminated {
			return "the session is live again"
		}
		sv, why := salvage.step(ctx, rec, true)
		if keep := inspectWorktrees(ctx, root, inspect, worktrees, false, sv); keep != "" {
			return withWhy(keep, why)
		}
		return ""
	}
}

// withWhy adds why salvage did not apply to a keep reason.
func withWhy(keep, why string) string {
	if why == "" {
		return keep
	}
	return keep + "; not salvaged: " + why
}

// inspectWorktrees returns why the worktrees must be kept. With a salvage
// step, work GitInspect would keep is salvaged (or, while planning, would be)
// instead; a status that cannot be read still keeps the worktree.
func inspectWorktrees(ctx context.Context, root string, inspect WorktreeInspector, paths []string, requireMerged bool, sv *salvageStep) string {
	if len(paths) == 0 {
		return "worktree status could not be read"
	}
	for _, path := range paths {
		if !pathx.Within(root, path) || samePath(root, path) {
			return "outside the managed worktree directory"
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		reason, err := inspect(ctx, path, requireMerged)
		if err != nil {
			return "worktree status could not be read"
		}
		if reason != "" && sv != nil {
			if sv.apply == nil {
				continue
			}
			if err := sv.apply(ctx, path, reason); err != nil {
				return "its work could not be salvaged: " + err.Error()
			}
			continue
		}
		if reason != "" {
			if len(paths) > 1 {
				if rel, err := filepath.Rel(paths[0], path); err == nil && rel != "." {
					reason += " in " + filepath.ToSlash(rel)
				}
			}
			return reason
		}
	}
	return ""
}

// teardownCheck is the WorktreeCheck the session manager runs under the
// session's guard right before teardown: the retention window against the
// fresh record and the full inspection of every repo again, so nothing that
// changed since this run's snapshot is torn down on the strength of it.
func (r *WorktreeRule) teardownCheck(run *Run) WorktreeCheck {
	policy := run.Policy.Worktrees
	retention := time.Duration(policy.RetentionDays) * 24 * time.Hour
	return func(ctx context.Context, rec domain.SessionRecord, worktrees []string) string {
		switch {
		case !rec.IsTerminated:
			return "the session is live again"
		case run.Now.Sub(rec.UpdatedAt) < retention:
			return fmt.Sprintf("ended less than %d day(s) ago", policy.RetentionDays)
		}
		sv, why := r.salvage.step(ctx, rec, true)
		return withWhy(r.inspectAll(ctx, worktrees, policy.RequireMerged, sv), why)
	}
}

// Run implements Rule.
func (r *WorktreeRule) Run(ctx context.Context, run *Run) error {
	if r.root == "" || r.remover == nil {
		return errors.New("worktree cleanup is not configured")
	}
	retention := time.Duration(run.Policy.Worktrees.RetentionDays) * 24 * time.Hour
	r.salvage.pruneSalvage(ctx, run)
	for _, rec := range run.Sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := rec.Metadata.WorkspacePath
		if !rec.IsTerminated || rec.IsTaskPreparation || path == "" {
			continue
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		item := Item{SessionID: rec.ID, ProjectID: rec.ProjectID, What: "worktree", Path: path, Status: StatusKept}
		switch {
		case !pathx.Within(r.root, path) || samePath(r.root, path):
			item.Reason = "outside the managed worktree directory"
			run.Add(item)
			continue
		case run.Now.Sub(rec.UpdatedAt) < retention:
			item.Reason = fmt.Sprintf("ended less than %d day(s) ago", run.Policy.Worktrees.RetentionDays)
			run.Add(item)
			continue
		case run.LiveWorkspace(path):
			item.Reason = "in use by a live session"
			run.Add(item)
			continue
		case run.Pass == PassLight && r.recentlyRefused(rec.ID, run.Now):
			item.Reason = "kept at the last attempt; retried in the daily pass"
			// An actionable reason stays visible (and flagged) between full passes.
			if last := r.keptReason(rec.ID); keptReasonKey(last) != "" {
				item.Reason, item.Attention = last, true
			}
			run.Add(item)
			continue
		}
		paths, err := r.remover.WorktreePaths(ctx, rec.ID)
		if err != nil {
			item.Reason = "worktree status could not be read"
			run.Add(item)
			continue
		}
		sv, why := r.salvage.step(ctx, rec, false)
		if keep := r.inspectAll(ctx, paths, run.Policy.Worktrees.RequireMerged, sv); keep != "" {
			item.Reason = withWhy(keep, why)
			r.noteKept(ctx, run, &item)
			if !run.DryRun {
				r.noteRefusal(rec.ID, run.Now, item.Reason)
			}
			run.Add(item)
			continue
		}
		if item.Bytes, err = dirSize(ctx, path); err != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if run.DryRun {
			item.Status, item.Reason = StatusWouldFree, ignoredFilesNote
			run.Add(item)
			continue
		}
		// The inspection above is only a plan: the session manager runs it
		// again under the session's guard, right before teardown.
		outcome, err := r.remover.CleanupSession(ctx, rec.ID, r.teardownCheck(run))
		switch {
		case err != nil:
			item.Status, item.Reason = StatusFailed, err.Error()
			r.noteRefusal(rec.ID, run.Now, item.Reason)
		case outcome.Kept != "":
			item.Bytes, item.Reason = 0, outcome.Kept
			r.noteKept(ctx, run, &item)
			r.noteRefusal(rec.ID, run.Now, item.Reason)
		case outcome.AlreadyGone:
			item.Bytes = 0
			item.Status, item.Reason = StatusKept, "already removed"
			r.clearRefusal(rec.ID)
		default:
			item.Status, item.Reason = StatusFreed, ignoredFilesNote
			r.clearRefusal(rec.ID)
		}
		run.Add(item)
		if !run.Pace(ctx) {
			return ctx.Err()
		}
	}
	return nil
}

// escapeGlob quotes the glob metacharacters git's --exclude would interpret,
// so a branch name only ever excludes itself.
func escapeGlob(name string) string {
	var b strings.Builder
	for _, r := range name {
		if strings.ContainsRune(`*?[]\\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// sameRealPath compares two paths after resolving symlinks (/var and
// /private/var on macOS).
func sameRealPath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return samePath(a, b)
	}
	return samePath(ra, rb)
}
