package session

import (
	"context"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/observe/scm"
)

// workspaceVisitedBranches is a seam: real git cannot be made to fail only here.
var workspaceVisitedBranches = scm.WorkspaceVisitedBranches

// WorkerBranchWork reads, live from git, whether any branch the session owns
// (local or pushed, including sibling PR branches under its namespace and any
// branch its worktree has been on) has commits not on its base. The persisted BranchState covers only the checked-out
// branch and can lag, so it is not enough to justify closing a worker. Anything
// it cannot read is reported as Unknown, which keeps the worker.
func (s *Service) WorkerBranchWork(ctx context.Context, rec domain.SessionRecord) domain.WorkerBranchWork {
	root := rec.Metadata.WorkspacePath
	if root == "" || isStandaloneScratchWorkspace(rec) {
		return domain.WorkerBranchWork{Unknown: "no git workspace"}
	}
	project, ok, err := s.sessionProject(ctx, rec)
	if err != nil {
		return domain.WorkerBranchWork{Unknown: "project lookup failed"}
	}
	if ok && project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return domain.WorkerBranchWork{Unknown: "multi-repo workspace"}
	}
	_, branch, head, ok2 := readGitHead(root)
	if !ok2 {
		return domain.WorkerBranchWork{Unknown: "workspace is not readable as git"}
	}
	out, err := gitWorkspaceOutput(ctx, root, "for-each-ref", "--format=%(objectname) %(refname)", "refs/heads", "refs/remotes/"+branchStateRemote)
	if err != nil {
		return domain.WorkerBranchWork{Unknown: "git ref listing failed"}
	}
	tips := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, cut := strings.Cut(strings.TrimSpace(line), " "); cut {
			tips[ref] = sha
		}
	}
	base := strings.TrimSpace(rec.Metadata.DiffBaseSHA)
	for _, ref := range branchBaseRefs(rec.Metadata.DiffBaseRef, defaultBranchForProject(project, ok)) {
		if sha := tips[ref]; sha != "" {
			base = sha
			break
		}
	}
	if base == "" {
		return domain.WorkerBranchWork{Unknown: "base branch not found"}
	}
	visited, ok3 := workspaceVisitedBranches(ctx, root)
	if !ok3 {
		return domain.WorkerBranchWork{Unknown: "HEAD reflog unreadable"}
	}
	return unmergedSessionRefs(ctx, rec, root, base, branch, head, tips, visited)
}

// unmergedSessionRefs counts commits past base for every session-owned ref.
func unmergedSessionRefs(ctx context.Context, rec domain.SessionRecord, root, base, branch, head string, tips map[string]string, visited map[string]struct{}) domain.WorkerBranchWork {
	var work domain.WorkerBranchWork
	checked := map[string]int{}
	count := func(sha string) (int, bool) {
		if n, seen := checked[sha]; seen {
			return n, true
		}
		n, ok := gitRevListCount(ctx, root, base+".."+sha)
		if ok {
			checked[sha] = n
		}
		return n, ok
	}
	if branch == "" && head != "" {
		n, ok := count(head)
		if !ok {
			return domain.WorkerBranchWork{Unknown: "commit count failed for HEAD"}
		}
		if n > 0 {
			work.Unmerged = append(work.Unmerged, "HEAD")
		}
	}
	for ref, sha := range tips {
		name, ok := sessionRefName(ref)
		if !ok {
			continue
		}
		if _, wasOn := visited[name]; !wasOn && name != branch && !scm.SessionOwnsBranch(rec, false, name) {
			continue
		}
		n, ok := count(sha)
		if !ok {
			return domain.WorkerBranchWork{Unknown: "commit count failed for " + ref}
		}
		if n > 0 {
			work.Unmerged = append(work.Unmerged, strings.TrimPrefix(ref, "refs/"))
		}
	}
	sort.Strings(work.Unmerged)
	return work
}

func sessionRefName(ref string) (string, bool) {
	if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
		return name, true
	}
	name, ok := strings.CutPrefix(ref, "refs/remotes/"+branchStateRemote+"/")
	return name, ok && name != "HEAD"
}
