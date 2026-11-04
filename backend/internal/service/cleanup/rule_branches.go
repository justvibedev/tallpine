package cleanup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/brand"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	tallpineprocess "tallpine.eu/tallpine/backend/internal/process"
)

// gitTimeout bounds one git call. Remote deletion talks to the network.
const gitTimeout = time.Minute

// guardedNetTimeout bounds a network call made while the session manager's
// guard holds the project's workspace gate, which stalls every spawn and
// restore in the project: the confirm right before deleting and the remote
// push. The listing the candidates were judged on runs before, outside the
// gate, with gitTimeout. A confirm that times out keeps the local branch; a
// push that times out is reported failed.
const guardedNetTimeout = 15 * time.Second

// BranchSources lists what the branch rule attributes branches with.
type BranchSources interface {
	ListProjects(ctx context.Context) ([]domain.ProjectRecord, error)
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
	// GetSession re-reads a session right before one of its branches is deleted.
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	// ListSessionWorktrees lists a session's per-repo worktrees, whose
	// branches and bases a live session uses.
	ListSessionWorktrees(ctx context.Context, id domain.SessionID) ([]domain.SessionWorktreeRecord, error)
}

// BranchGuard is the session manager's coordination for deleting a terminated
// session's branches: GuardBranchCleanup runs fn while no session of the
// project can restore or spawn, after re-reading the session under its guard.
// A non-empty reason, or an error, means fn did not run.
type BranchGuard interface {
	GuardBranchCleanup(ctx context.Context, id domain.SessionID, fn func(context.Context)) (string, error)
}

// GitRunner runs git in repo and returns its trimmed stdout.
type GitRunner func(ctx context.Context, repo string, args ...string) (string, error)

// RunGit is the production GitRunner.
func RunGit(ctx context.Context, repo string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := ports.DefaultGit().Plain(ctx, repo, args...)
	// A timeout or cancel stops what git started too (ssh, credential
	// helpers, askpass), not only git: one stuck on a prompt would otherwise
	// be orphaned on every pass.
	tallpineprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = 5 * time.Second
	// Never prompt for credentials from a background pass; core.sshCommand is untouched.
	cmd.Env = append(cmd.Env, "SSH_ASKPASS_REQUIRE=never")
	stdout, stderr := &capWriter{max: maxGitStdout}, &capWriter{max: maxGitStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		msg := bytes.TrimSpace(stderr.buf.Bytes())
		if stderr.over {
			msg = append(msg, []byte(" [...]")...)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	// A cut-off listing (of refs, worktrees, preserved work) is unsafe to
	// judge from: fail instead.
	if stdout.over {
		return "", fmt.Errorf("git %s: output exceeded %d bytes", strings.Join(args, " "), maxGitStdout)
	}
	return strings.TrimSpace(stdout.buf.String()), nil
}

// Output bounds for one git call: stderr ends up in a persisted reason.
const (
	maxGitStdout = 64 << 20
	maxGitStderr = 4 << 10
)

// capWriter keeps the first max bytes and notes that more were written. It
// never fails a write, so git is not cut off mid-run.
type capWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); len(p) > room {
		w.over = true
		w.buf.Write(p[:max(room, 0)])
	} else {
		w.buf.Write(p)
	}
	return len(p), nil
}

// BranchRule deletes the tp/ branches of terminated worker sessions once
// their work is safe elsewhere:
//
//   - merged: the tip is contained in another remote branch the server
//     confirms, or it is the head of the session's merged PR (squash merges leave no common history) -- after
//     MergedRetentionDays;
//   - pushed: every commit is on a remote branch, which the server confirms
//     still has them -- after StaleRetentionDays.
//
// A branch with commits that exist nowhere else, one checked out in any
// worktree, or one whose session has preserved uncommitted work
// (refs/ao/preserved/<id>) is kept. Only refs/heads/tp/... branches that name
// a known worker session of the project are considered, and a deletion only
// succeeds while the branch still points at the tip that was judged. Remote
// branches are only deleted when DeleteRemote is on, only for a merged PR,
// and only while the remote branch is still at the PR's head. While the policy
// is off, the rule only reports what it would delete.
type BranchRule struct {
	src   BranchSources
	guard BranchGuard
	git   GitRunner
	// guardedNet is guardedNetTimeout; tests shorten it.
	guardedNet time.Duration
}

// NewBranchRule builds the rule; a nil git uses RunGit. Without a guard the
// rule reports but never deletes.
func NewBranchRule(src BranchSources, guard BranchGuard, git GitRunner) *BranchRule {
	if git == nil {
		git = RunGit
	}
	return &BranchRule{src: src, guard: guard, git: git, guardedNet: guardedNetTimeout}
}

// Category implements Rule.
func (*BranchRule) Category() Category { return CategoryBranches }

// Light implements Rule.
func (*BranchRule) Light() bool { return false }

// Enabled implements Rule: the rule always runs, and only deletes anything
// once the policy turns it on; while off it reports what it would delete.
func (*BranchRule) Enabled(Policy) bool { return true }

type branchRef struct {
	name     string // tp/<...>, without refs/heads/
	tip      string
	worktree string
	// onServer is the remote branch that vouches for the tip when the
	// deletion relies on the server; it is asked again right before deleting.
	onServer *remoteRef
}

// sessionOfBranch returns the worker session a tp/ branch belongs to: the
// first path segment after the prefix (and an optional namespace) that is a
// session id.
func sessionOfBranch(branch string, sessions map[domain.SessionID]domain.SessionRecord) (domain.SessionRecord, bool) {
	segs := strings.Split(branch, "/")
	if len(segs) < 2 || !slices.Contains(brand.BranchPrefixes(), segs[0]) {
		return domain.SessionRecord{}, false
	}
	for _, seg := range segs[1:min(len(segs), 3)] {
		if rec, ok := sessions[domain.SessionID(seg)]; ok {
			return rec, true
		}
	}
	return domain.SessionRecord{}, false
}

// Run implements Rule.
func (r *BranchRule) Run(ctx context.Context, run *Run) error {
	if r.src == nil {
		return errors.New("branch cleanup is not configured")
	}
	projects, err := r.src.ListProjects(ctx)
	if err != nil {
		return err
	}
	byProject := map[domain.ProjectID]map[domain.SessionID]domain.SessionRecord{}
	for _, rec := range run.Sessions {
		if rec.Kind != domain.KindWorker || rec.ProjectID == "" {
			continue
		}
		if byProject[rec.ProjectID] == nil {
			byProject[rec.ProjectID] = map[domain.SessionID]domain.SessionRecord{}
		}
		byProject[rec.ProjectID][rec.ID] = rec
	}
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return err
		}
		sessions := byProject[domain.ProjectID(project.ID)]
		if len(sessions) == 0 || project.Path == "" {
			continue
		}
		if info, err := os.Stat(project.Path); err != nil || !info.IsDir() {
			continue
		}
		if err := r.project(ctx, run, project, sessions); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			run.Add(Item{ProjectID: domain.ProjectID(project.ID), What: "branches", Path: project.Path, Status: StatusFailed, Reason: err.Error()})
		}
	}
	return ctx.Err()
}

func (r *BranchRule) project(ctx context.Context, run *Run, project domain.ProjectRecord, sessions map[domain.SessionID]domain.SessionRecord) error {
	repo := project.Path
	prefixes := brand.BranchPrefixes()
	headArgs := append(make([]string, 0, 2+len(prefixes)), "for-each-ref", "--format=%(refname)%00%(objectname)%00%(worktreepath)")
	preservedArgs := append(make([]string, 0, 3+len(prefixes)), "for-each-ref", "--format=%(refname)", "refs/ao/preserved/")
	for _, prefix := range prefixes {
		headArgs = append(headArgs, "refs/heads/"+prefix+"/")
		preservedArgs = append(preservedArgs, "refs/"+prefix+"/preserved/")
	}
	out, err := r.git(ctx, repo, headArgs...)
	if err != nil {
		return err
	}
	preserved := map[domain.SessionID]bool{}
	if refs, err := r.git(ctx, repo, preservedArgs...); err == nil {
		for _, ref := range strings.Fields(refs) {
			preserved[domain.SessionID(ref[strings.LastIndex(ref, "/")+1:])] = true
		}
	} else {
		return err
	}
	prs := map[domain.SessionID][]domain.PullRequest{}
	heads := &serverHeads{}
	for _, line := range strings.Split(out, "\n") {
		if err := ctx.Err(); err != nil {
			return err
		}
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 {
			continue
		}
		ref := branchRef{name: strings.TrimPrefix(fields[0], "refs/heads/"), tip: fields[1], worktree: fields[2]}
		rec, ok := sessionOfBranch(ref.name, sessions)
		if !ok || !rec.IsTerminated {
			continue
		}
		if _, cached := prs[rec.ID]; !cached {
			list, err := r.src.ListPRsBySession(ctx, rec.ID)
			if err != nil {
				return err
			}
			prs[rec.ID] = list
		}
		r.branch(ctx, run, repo, heads, rec, ref, preserved[rec.ID], prs[rec.ID])
	}
	return nil
}

// remoteRef is a remote-tracking branch: remote and branch name.
type remoteRef struct{ remote, branch string }

// containment reports where else a tip lives: mergedRemotes are the
// remote-tracking branches of other names that contain it, remotes all that
// do, the session's own included. Remote-tracking refs are only a cache, so
// what they show is confirmed with the server (onServer) before a deletion
// relies on it. Another local branch is no evidence: its commits may not be on
// the server, and a later restore of the session would come back without
// them.
func (r *BranchRule) containment(ctx context.Context, repo string, ref branchRef, sessions func(string) bool) (mergedRemotes, remotes []remoteRef, err error) {
	out, err := r.git(ctx, repo, "for-each-ref", "--contains", ref.tip, "--format=%(refname)", "refs/remotes/")
	if err != nil {
		return nil, nil, err
	}
	for _, name := range strings.Fields(out) {
		remote, branch, ok := strings.Cut(strings.TrimPrefix(name, "refs/remotes/"), "/")
		if !ok || branch == "HEAD" {
			continue
		}
		rr := remoteRef{remote: remote, branch: branch}
		remotes = append(remotes, rr)
		if !sessions(branch) {
			mergedRemotes = append(mergedRemotes, rr)
		}
	}
	return mergedRemotes, remotes, nil
}

// serverHeads caches one listing of each remote's branches per project pass,
// a failed listing included: an unreachable remote then costs one timeout
// per project, not one per branch.
type serverHeads struct {
	lists map[string]serverList
}

type serverList struct {
	heads map[string]string // branch -> sha
	err   error
}

func (h *serverHeads) list(ctx context.Context, r *BranchRule, repo, remote string) serverList {
	if l, ok := h.lists[remote]; ok {
		return l
	}
	l := serverList{heads: map[string]string{}}
	if out, err := r.git(ctx, repo, "ls-remote", "--heads", remote); err != nil {
		l.err = err
	} else {
		for _, line := range strings.Split(out, "\n") {
			if fields := strings.Fields(line); len(fields) == 2 {
				if branch, ok := strings.CutPrefix(fields[1], "refs/heads/"); ok {
					l.heads[branch] = fields[0]
				}
			}
		}
	}
	if h.lists == nil {
		h.lists = map[string]serverList{}
	}
	h.lists[remote] = l
	return l
}

// onServer confirms with the server that one of the remote branches still
// contains the tip and returns it, or nil and why the branch is kept.
// Remote-tracking refs are only a cache: one left behind by a branch deleted
// on the server (a PR closed with "delete branch") would vouch for commits
// that exist nowhere else, and the next fetch --prune would drop it. The
// server is listed once per project pass; the branch a deletion relies on is
// asked again right before deleting (confirmOnServer). Any error keeps the
// branch.
func (r *BranchRule) onServer(ctx context.Context, repo string, heads *serverHeads, ref branchRef, remotes []remoteRef) (*remoteRef, string) {
	for _, rr := range remotes {
		if strings.HasPrefix(rr.remote, "-") {
			continue
		}
		l := heads.list(ctx, r, repo, rr.remote)
		if sha, ok := l.heads[rr.branch]; ok && l.err == nil && r.contains(ctx, repo, sha, ref.tip) {
			return &rr, ""
		}
	}
	return nil, "no branch on the remote server still has its commits"
}

// confirmOnServer asks the server again, for the one branch a deletion relies
// on, whether it still contains the tip.
func (r *BranchRule) confirmOnServer(ctx context.Context, repo string, ref branchRef) bool {
	rr := ref.onServer
	ctx, cancel := context.WithTimeout(ctx, r.guardedNet)
	defer cancel()
	out, err := r.git(ctx, repo, "ls-remote", rr.remote, "refs/heads/"+rr.branch)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == "refs/heads/"+rr.branch && r.contains(ctx, repo, fields[0], ref.tip) {
			return true
		}
	}
	return false
}

// contains reports whether the server's sha is tip or a descendant of it. It
// fails, and so keeps the branch, also when the server's commit was never
// fetched here and cannot be checked.
func (r *BranchRule) contains(ctx context.Context, repo, sha, tip string) bool {
	if sha == tip {
		return true
	}
	_, err := r.git(ctx, repo, "merge-base", "--is-ancestor", tip, sha)
	return err == nil
}

// usesBranch reports whether rec, a live session other than owner, uses
// branch of project: as its branch or diff base, as the branch or base of one
// of its repo worktrees, or possibly, while it is still being set up in the
// project (provisioning, or a worker with no branch or workspace yet) and its
// branch may not be recorded yet. A worktree read error counts as a use.
func (r *BranchRule) usesBranch(ctx context.Context, rec domain.SessionRecord, owner domain.SessionID, project domain.ProjectID, branch string) bool {
	if rec.IsTerminated || rec.ID == owner {
		return false
	}
	names := func(ref string) bool {
		ref = strings.TrimSpace(ref)
		return ref == branch || ref == "refs/heads/"+branch
	}
	md := rec.Metadata
	if names(md.Branch) || names(md.DiffBaseRef) {
		return true
	}
	if rec.ProjectID == project && (rec.ProvisionState.IsProvisioning() || (rec.Kind == domain.KindWorker && md.Branch == "" && md.WorkspacePath == "")) {
		return true
	}
	rows, err := r.src.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return true
	}
	for _, row := range rows {
		if names(row.Branch) || names(row.BaseRef) {
			return true
		}
	}
	return false
}

// liveUser returns a live session in the snapshot that uses branch, or "".
func (r *BranchRule) liveUser(ctx context.Context, run *Run, owner domain.SessionID, project domain.ProjectID, branch string) domain.SessionID {
	for _, rec := range run.Sessions {
		if r.usesBranch(ctx, rec, owner, project, branch) {
			return rec.ID
		}
	}
	return ""
}

// confirmedMerge reports whether a branch of another name on the server
// contains the tip, and records it on ref.
func (r *BranchRule) confirmedMerge(ctx context.Context, repo string, heads *serverHeads, ref *branchRef, remotes []remoteRef) bool {
	rr, _ := r.onServer(ctx, repo, heads, *ref, remotes)
	ref.onServer = rr
	return rr != nil
}

func mergedPR(prs []domain.PullRequest, ref branchRef) (domain.PullRequest, bool) {
	for _, pr := range prs {
		if pr.Merged && pr.SourceBranch == ref.name && pr.HeadSHA != "" && pr.HeadSHA == ref.tip {
			return pr, true
		}
	}
	return domain.PullRequest{}, false
}

func (r *BranchRule) branch(ctx context.Context, run *Run, repo string, heads *serverHeads, rec domain.SessionRecord, ref branchRef, preserved bool, prs []domain.PullRequest) {
	policy := run.Policy.Branches
	item := Item{SessionID: rec.ID, ProjectID: rec.ProjectID, What: "branch " + ref.name, Path: repo, Status: StatusKept}
	if ref.worktree != "" {
		item.Reason = "checked out in a worktree"
		run.Add(item)
		return
	}
	if preserved {
		item.Reason = "the session has preserved uncommitted work"
		run.Add(item)
		return
	}
	ownBranch := func(name string) bool {
		owner, ok := sessionOfBranch(name, map[domain.SessionID]domain.SessionRecord{rec.ID: rec})
		return ok && owner.ID == rec.ID
	}
	mergedRemotes, remotes, err := r.containment(ctx, repo, ref, ownBranch)
	if err != nil {
		item.Status, item.Reason = StatusFailed, err.Error()
		run.Add(item)
		return
	}
	pr, viaPR := mergedPR(prs, ref)
	// Merged as far as this clone knows; on remote evidence alone it is
	// confirmed with the server before the branch goes.
	merged := viaPR || len(mergedRemotes) > 0
	pushed := len(remotes) > 0
	age := run.Now.Sub(rec.UpdatedAt)
	days := func(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
	if user := r.liveUser(ctx, run, rec.ID, rec.ProjectID, ref.name); user != "" {
		item.Reason = "live session " + string(user) + " uses this branch"
		run.Add(item)
		return
	}
	switch {
	case merged && age < days(policy.MergedRetentionDays):
		item.Reason = fmt.Sprintf("merged; deleted %d day(s) after the session ended", policy.MergedRetentionDays)
		run.Add(item)
		return
	case viaPR:
		item.Reason = "merged: the head of the session's merged PR"
	case merged && r.confirmedMerge(ctx, repo, heads, &ref, mergedRemotes):
		item.Reason = "merged: contained in another branch on the remote server"
	case pushed && age >= days(policy.StaleRetentionDays):
		rr, reason := r.onServer(ctx, repo, heads, ref, remotes)
		if rr == nil {
			item.Reason = reason
			run.Add(item)
			return
		}
		ref.onServer = rr
		item.Reason = "unmerged, every commit is on a remote branch"
	case pushed:
		item.Reason = fmt.Sprintf("unmerged; deleted %d day(s) after the session ended", policy.StaleRetentionDays)
		run.Add(item)
		return
	default:
		item.Reason = "has commits that exist nowhere else"
		run.Add(item)
		return
	}
	r.delete(ctx, run, repo, ref, item, pr, policy.DeleteRemote && viaPR)
	_ = run.Pace(ctx)
}

// delete deletes the judged local branch and, with remote, the merged PR's
// remote branch. While the policy is off it only reports, and a dry run
// reports what it would delete. A real deletion runs under the session
// manager's guard, so no restore or spawn in the project runs between the
// final checks and the last ref mutation, the remote push included.
func (r *BranchRule) delete(ctx context.Context, run *Run, repo string, ref branchRef, item Item, pr domain.PullRequest, remote bool) {
	switch {
	case !run.Policy.Branches.Enabled:
		item.Status = StatusNeedsConfirmation
		item.Reason += "; branch cleanup is off, turn on branches.enabled to delete these"
		run.Add(item)
		return
	case run.DryRun:
		item.Status = StatusWouldFree
		run.Add(item)
		if remote {
			r.deleteRemote(ctx, run, repo, ref, pr, item)
		}
		return
	case r.guard == nil:
		item.Reason = "branch deletion is not coordinated with session restore"
		run.Add(item)
		return
	}
	kept, err := r.guard.GuardBranchCleanup(ctx, item.SessionID, func(ctx context.Context) {
		// The remote branch goes only with the local one: whatever kept the
		// local branch (a live session, a new checkout, a moved tip) keeps it
		// too.
		if r.deleteLocal(ctx, run, repo, ref, item) && remote {
			r.deleteRemote(ctx, run, repo, ref, pr, item)
		}
	})
	switch {
	case err != nil:
		item.Status, item.Reason = StatusFailed, err.Error()
		run.Add(item)
	case kept != "":
		item.Reason = kept
		run.Add(item)
	}
}

// deleteLocal makes the final checks and deletes the local branch; it reports
// whether it did. It runs under the session manager's guard.
func (r *BranchRule) deleteLocal(ctx context.Context, run *Run, repo string, ref branchRef, item Item) bool {
	if cur, found, err := r.src.GetSession(ctx, item.SessionID); err != nil || (found && !cur.IsTerminated) {
		item.Reason = "the session is live again"
		run.Add(item)
		return false
	}
	// Re-read every session: one spawned onto, or stacked on, this branch since
	// the snapshot keeps it, even before its worktree exists.
	if run.AnyLive(ctx, func(rec domain.SessionRecord) bool {
		return r.usesBranch(ctx, rec, item.SessionID, item.ProjectID, ref.name)
	}) {
		item.Reason = "a live session uses this branch"
		run.Add(item)
		return false
	}
	// Re-check right before deleting: a branch checked out meanwhile stays.
	worktrees, err := r.git(ctx, repo, "worktree", "list", "--porcelain")
	if err != nil {
		item.Status, item.Reason = StatusFailed, err.Error()
		run.Add(item)
		return false
	}
	for _, line := range strings.Split(worktrees, "\n") {
		if strings.TrimSpace(line) == "branch refs/heads/"+ref.name {
			item.Reason = "checked out in a worktree"
			run.Add(item)
			return false
		}
	}
	// The server listing is from the start of the project's pass: ask again
	// for the branch the deletion relies on.
	if ref.onServer != nil && !r.confirmOnServer(ctx, repo, ref) {
		item.Reason = "the remote server no longer has its commits"
		run.Add(item)
		return false
	}
	// update-ref with the old value deletes only if the branch still points at
	// the tip that was judged.
	if _, err := r.git(ctx, repo, "update-ref", "-d", "refs/heads/"+ref.name, ref.tip); err != nil {
		item.Status, item.Reason = StatusFailed, err.Error()
		run.Add(item)
		return false
	}
	item.Status = StatusFreed
	run.Add(item)
	return true
}

// prRemote returns "" when every URL remote fetches from and every
// destination a push to it reaches is the merged PR's repository, or why the
// remote branch is kept: another remote, or one that git resolves elsewhere
// (a pushurl to a fork, several push URLs, an insteadOf or pushInsteadOf
// rewrite), may carry the same branch at the same tip. git remote get-url
// applies all of that, and includes and env config, the way the push will.
// Anything that cannot be matched keeps.
func (r *BranchRule) prRemote(ctx context.Context, repo, remote string, pr domain.PullRequest) string {
	const other = "not the merged PR's repository"
	if pr.Host == "" || pr.Repo == "" {
		return "the merged PR's repository is unknown"
	}
	if strings.HasPrefix(remote, "-") {
		return other
	}
	for _, args := range [][]string{{"remote", "get-url", "--all", remote}, {"remote", "get-url", "--push", "--all", remote}} {
		out, err := r.git(ctx, repo, args...)
		urls := strings.Fields(out)
		if err != nil || len(urls) == 0 {
			return other
		}
		for _, u := range urls {
			if !samePRRepo(u, pr) {
				return other
			}
		}
	}
	return ""
}

func samePRRepo(raw string, pr domain.PullRequest) bool {
	id, err := domain.ParseRepositoryIdentity(raw)
	return err == nil && strings.EqualFold(id.Host, pr.Host) && domain.SameRepoPath(id.Namespace+"/"+id.Name, pr.Repo)
}

func (r *BranchRule) deleteRemote(ctx context.Context, run *Run, repo string, ref branchRef, pr domain.PullRequest, local Item) {
	out, err := r.git(ctx, repo, "for-each-ref", "--format=%(refname)%00%(objectname)", "refs/remotes/")
	if err != nil {
		item := local
		item.What, item.Status, item.Reason = "remote branch "+ref.name, StatusFailed, "remote branches could not be listed: "+err.Error()
		run.Add(item)
		return
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 2 {
			continue
		}
		rest := strings.TrimPrefix(fields[0], "refs/remotes/")
		i := strings.Index(rest, "/")
		if i < 0 || rest[i+1:] != ref.name {
			continue
		}
		remote := rest[:i]
		item := local
		item.What, item.Status = "remote branch "+remote+"/"+ref.name, StatusKept
		if fields[1] != pr.HeadSHA {
			item.Reason = "the remote branch moved past the merged PR"
			run.Add(item)
			continue
		}
		if reason := r.prRemote(ctx, repo, remote, pr); reason != "" {
			item.Reason = reason
			run.Add(item)
			continue
		}
		if run.DryRun {
			item.Status = StatusWouldFree
			run.Add(item)
			continue
		}
		// The lease makes the push a no-op unless the remote branch is still
		// at the PR's head. A delete-only push has nothing for a pre-push hook
		// to check, and a background pass must not run project code. It runs
		// under the guard, so it is bounded short.
		pushCtx, cancel := context.WithTimeout(ctx, r.guardedNet)
		_, err := r.git(pushCtx, repo, "push", "--no-verify", "--force-with-lease=refs/heads/"+ref.name+":"+pr.HeadSHA, remote, ":refs/heads/"+ref.name)
		cancel()
		if err != nil {
			item.Status, item.Reason = StatusFailed, err.Error()
		} else {
			item.Status, item.Reason = StatusFreed, "merged PR"
		}
		run.Add(item)
	}
}
