package session

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

func (s *Service) sessionWorkspaceRecord(ctx context.Context, id domain.SessionID) (domain.SessionRecord, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if strings.TrimSpace(rec.Metadata.WorkspacePath) == "" {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	info, err := os.Stat(rec.Metadata.WorkspacePath)
	if err != nil || !info.IsDir() {
		return domain.SessionRecord{}, apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	return rec, nil
}

func (s *Service) sessionProject(ctx context.Context, rec domain.SessionRecord) (domain.ProjectRecord, bool, error) {
	if s.store == nil || rec.ProjectID == "" {
		return domain.ProjectRecord{}, false, nil
	}
	project, ok, err := s.store.GetProject(ctx, string(rec.ProjectID))
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", rec.ProjectID, err)
	}
	return project, ok, nil
}

func defaultBranchForProject(project domain.ProjectRecord, ok bool) string {
	if !ok {
		return ""
	}
	return project.Config.WorktreeBaseBranch()
}

type workspaceCompareTarget struct {
	BaseSHA string
	BaseRef string
	Mode    WorkspaceCompareMode
}

func (c workspaceCompareTarget) gitBase() string {
	if c.Mode == WorkspaceCompareBase && strings.TrimSpace(c.BaseSHA) != "" {
		return c.BaseSHA
	}
	return "HEAD"
}

func (s *Service) workspaceComparePRs(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	if s.store == nil {
		return nil, nil
	}
	prs, err := s.store.ListPRsBySession(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list PRs for compare base: %w", err)
	}
	return prs, nil
}

func resolveWorkspaceCompare(ctx context.Context, root, recordedSHA, recordedRef, defaultBranch string, prs []domain.PullRequest) workspaceCompareTarget {
	recordedSHA = strings.TrimSpace(recordedSHA)
	recordedRef = strings.TrimSpace(recordedRef)

	// The best available local candidate: the live, ref-derived merge base
	// (e.g. origin/main) when it resolves, else the session's spawn-time
	// recorded base. Both are re-derived through git merge-base — never used
	// as a raw revision — so a rewritten or unrelated commit can't become the
	// diff base outright.
	var localTarget *workspaceCompareTarget
	if recordedRef != "" && recordedRef != "HEAD" {
		for _, ref := range workspaceBaseRefCandidates(recordedRef) {
			if sha, ok := mergeBaseCandidate(ctx, root, ref); ok {
				target := workspaceCompareTarget{BaseSHA: sha, BaseRef: ref, Mode: WorkspaceCompareBase}
				localTarget = &target
				break
			}
		}
	}
	if localTarget == nil {
		if sha, ok := mergeBaseCandidate(ctx, root, recordedSHA); ok {
			target := workspaceCompareTarget{BaseSHA: sha, BaseRef: recordedRef, Mode: WorkspaceCompareBase}
			localTarget = &target
		}
	}

	// The provider PR's own, independently-synced base. A local
	// remote-tracking ref can go stale (Tallpine never fetches a session
	// worktree), so this can be more advanced than the local candidate above
	// — but it's equally re-derived through merge-base rather than trusted as
	// a raw revision, since pr.BaseSHA is the target branch's current tip,
	// not necessarily an ancestor of HEAD.
	var prTarget *workspaceCompareTarget
	var prHeadSHA string
	if pr, ok := selectWorkspaceComparePR(prs, defaultBranch); ok {
		if sha, ok := mergeBaseCandidate(ctx, root, pr.BaseSHA); ok {
			target := workspaceCompareTarget{BaseSHA: sha, BaseRef: strings.TrimSpace(pr.TargetBranch), Mode: WorkspaceCompareBase}
			prTarget = &target
			prHeadSHA = strings.TrimSpace(pr.HeadSHA)
		}
	}

	switch {
	case localTarget != nil && prTarget != nil:
		if localTarget.BaseSHA == prTarget.BaseSHA {
			return *localTarget
		}
		if gitIsAncestor(ctx, root, localTarget.BaseSHA, prTarget.BaseSHA) {
			return *prTarget // PR candidate is the more advanced descendant.
		}
		if gitIsAncestor(ctx, root, prTarget.BaseSHA, localTarget.BaseSHA) {
			return *localTarget // Local candidate is the more advanced descendant.
		}
		// Neither candidate is an ancestor of the other — divergent
		// histories, e.g. the target branch was force-pushed to a
		// replacement line. Only trust the PR snapshot when it describes the
		// exact commit the Files tab is displaying; otherwise a stale
		// snapshot could silently outrank a valid local candidate.
		if head, ok := gitRevision(ctx, root, "HEAD"); ok && prHeadSHA != "" && prHeadSHA == head {
			return *prTarget
		}
		return *localTarget
	case prTarget != nil:
		return *prTarget
	case localTarget != nil:
		return *localTarget
	}

	for _, ref := range workspaceBaseRefCandidates(defaultBranch) {
		if sha, ok := gitMergeBase(ctx, root, ref); ok {
			return workspaceCompareTarget{BaseSHA: sha, BaseRef: ref, Mode: WorkspaceCompareBase}
		}
	}
	return workspaceCompareTarget{Mode: WorkspaceCompareHeadFallback}
}

func resolveWorkspaceProjectCompare(ctx context.Context, root, recordedSHA, recordedRef string) workspaceCompareTarget {
	recordedRef = workspaceDefaultBranch(recordedRef)
	for _, ref := range workspaceBaseRefCandidates(recordedRef) {
		if sha, ok := gitMergeBase(ctx, root, ref); ok {
			return workspaceCompareTarget{BaseSHA: sha, BaseRef: ref, Mode: WorkspaceCompareBase}
		}
	}
	recordedSHA = strings.TrimSpace(recordedSHA)
	if recordedSHA != "" && gitCommitExists(ctx, root, recordedSHA) {
		return workspaceCompareTarget{BaseSHA: recordedSHA, BaseRef: recordedRef, Mode: WorkspaceCompareBase}
	}
	return workspaceCompareTarget{Mode: WorkspaceCompareHeadFallback}
}

func workspaceBaseRefCandidates(defaultBranch string) []string {
	defaultBranch = workspaceDefaultBranch(defaultBranch)
	if defaultBranch == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var refs []string
	add := func(ref string) {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			return
		}
		if _, ok := seen[ref]; ok {
			return
		}
		seen[ref] = struct{}{}
		refs = append(refs, ref)
	}
	if !strings.HasPrefix(defaultBranch, "origin/") && !strings.HasPrefix(defaultBranch, "refs/") {
		add("origin/" + defaultBranch)
		add("refs/remotes/origin/" + defaultBranch)
	}
	add(defaultBranch)
	return refs
}

func workspaceDefaultBranch(defaultBranch string) string {
	defaultBranch = strings.TrimSpace(defaultBranch)
	if defaultBranch == domain.DefaultBranchAuto {
		return ""
	}
	return defaultBranch
}

func selectWorkspaceComparePR(prs []domain.PullRequest, defaultBranch string) (domain.PullRequest, bool) {
	if len(prs) == 0 {
		return domain.PullRequest{}, false
	}
	defaultBranch = strings.TrimSpace(defaultBranch)
	candidates := append([]domain.PullRequest(nil), prs...)
	sort.SliceStable(candidates, func(i, j int) bool {
		leftDefault := prTargetsDefaultBranch(candidates[i], defaultBranch)
		rightDefault := prTargetsDefaultBranch(candidates[j], defaultBranch)
		if leftDefault != rightDefault {
			return leftDefault
		}
		leftOpen := !candidates[i].Merged && !candidates[i].Closed
		rightOpen := !candidates[j].Merged && !candidates[j].Closed
		if leftOpen != rightOpen {
			return leftOpen
		}
		leftCreated := candidates[i].CreatedAtProvider
		rightCreated := candidates[j].CreatedAtProvider
		if !leftCreated.IsZero() && !rightCreated.IsZero() && !leftCreated.Equal(rightCreated) {
			return leftCreated.Before(rightCreated)
		}
		if candidates[i].Number != candidates[j].Number {
			if candidates[i].Number == 0 {
				return false
			}
			if candidates[j].Number == 0 {
				return true
			}
			return candidates[i].Number < candidates[j].Number
		}
		return candidates[i].URL < candidates[j].URL
	})
	for _, pr := range candidates {
		if strings.TrimSpace(pr.BaseSHA) != "" {
			return pr, true
		}
	}
	return domain.PullRequest{}, false
}

func prTargetsDefaultBranch(pr domain.PullRequest, defaultBranch string) bool {
	target := strings.TrimSpace(pr.TargetBranch)
	if target == "" || defaultBranch == "" {
		return false
	}
	if target == defaultBranch {
		return true
	}
	if !strings.HasPrefix(defaultBranch, "origin/") && !strings.HasPrefix(defaultBranch, "refs/") {
		return target == "origin/"+defaultBranch || target == "refs/heads/"+defaultBranch || target == "refs/remotes/origin/"+defaultBranch
	}
	return false
}

func gitCommitExists(ctx context.Context, root, rev string) bool {
	_, err := gitWorkspaceOutput(ctx, root, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

func gitMergeBase(ctx context.Context, root, ref string) (string, bool) {
	return gitMergeBaseOf(ctx, root, "HEAD", ref)
}

// gitMergeBaseOf is gitMergeBase for two arbitrary revisions rather than
// HEAD and one, so a view that is not checked out can find its own base.
func gitMergeBaseOf(ctx context.Context, root, a, b string) (string, bool) {
	out, err := gitWorkspaceOutput(ctx, root, "merge-base", a, b)
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(out)
	return sha, sha != ""
}

// gitIsAncestor reports whether ancestor is reachable from descendant
// (including ancestor == descendant).
func gitIsAncestor(ctx context.Context, root, ancestor, descendant string) bool {
	_, err := gitWorkspaceOutput(ctx, root, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

// mergeBaseCandidate validates a revision-derived compare-base candidate: the
// revision must exist locally and share a common ancestor with HEAD. It
// always returns the merge-base SHA, never the raw revision, so a rewritten
// or unrelated commit can never be used directly as a two-tree diff base.
func mergeBaseCandidate(ctx context.Context, root, revision string) (string, bool) {
	revision = strings.TrimSpace(revision)
	// A leading "-" would be parsed by git merge-base as an option (for example
	// --octopus exits 0 and prints HEAD), so reject it before it reaches git.
	if revision == "" || strings.HasPrefix(revision, "-") {
		return "", false
	}
	// merge-base itself fails for a revision that is not a local commit, so a
	// separate existence probe would only add a git process per candidate (the
	// comparison is resolved on every workspace call, and under load each
	// spawn is slow).
	return gitMergeBase(ctx, root, revision)
}

// gitRevision resolves rev to its full SHA, or ok=false if it cannot be
// resolved.
func gitRevision(ctx context.Context, root, rev string) (string, bool) {
	out, err := gitWorkspaceOutput(ctx, root, "rev-parse", "--verify", rev)
	if err != nil {
		return "", false
	}
	sha := strings.TrimSpace(out)
	return sha, sha != ""
}

func aggregateWorkspaceCompare(compares []workspaceCompareTarget) workspaceCompareTarget {
	if len(compares) == 0 {
		return workspaceCompareTarget{Mode: WorkspaceCompareHeadFallback}
	}
	baseFound := false
	fallbackFound := false
	baseSHA := ""
	baseRef := ""
	for _, compare := range compares {
		if compare.Mode != WorkspaceCompareBase {
			fallbackFound = true
			continue
		}
		if !baseFound {
			baseFound = true
			baseSHA = compare.BaseSHA
			baseRef = compare.BaseRef
			continue
		}
		if compare.BaseSHA != baseSHA {
			baseSHA = ""
		}
		if compare.BaseRef != baseRef {
			baseRef = ""
		}
	}
	if !baseFound {
		return workspaceCompareTarget{Mode: WorkspaceCompareHeadFallback}
	}
	if fallbackFound {
		baseSHA = ""
		baseRef = ""
	}
	return workspaceCompareTarget{BaseSHA: baseSHA, BaseRef: baseRef, Mode: WorkspaceCompareBase}
}
