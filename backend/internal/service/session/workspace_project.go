package session

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func (s *Service) listWorkspaceProjectFiles(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord) (WorkspaceFiles, error) {
	rows, err := s.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return WorkspaceFiles{}, fmt.Errorf("list workspace project rows: %w", err)
	}
	if len(rows) == 0 {
		prs, err := s.workspaceComparePRs(ctx, rec.ID)
		if err != nil {
			return WorkspaceFiles{}, err
		}
		resolve := func(rctx context.Context) workspaceCompareTarget {
			return resolveWorkspaceCompare(rctx, rec.Metadata.WorkspacePath, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, true), prs)
		}
		files, truncated, compare, err := s.workspaceFileSummariesCached(ctx, rec.ID, rec.Metadata.WorkspacePath, "", nil, resolve)
		if err != nil {
			return WorkspaceFiles{}, err
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
		result := WorkspaceFiles{SessionID: rec.ID, CompareBaseSHA: compare.BaseSHA, CompareBaseRef: compare.BaseRef, CompareMode: compare.Mode, Files: files, Truncated: truncated}
		uncommitted, err := workspaceUncommittedFiles(ctx, rec.Metadata.WorkspacePath, "", nil)
		if err != nil {
			if errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
				return WorkspaceFiles{}, err
			}
			result.Degraded = true
			result.DegradedCode = workspaceDegradedCode(err)
			return result, nil
		}
		result.Sections.Uncommitted = uncommitted
		return result, nil
	}

	prefixes := workspaceProjectPrefixes(rec.Metadata.WorkspacePath, rows)
	childPrefixes := nonEmptyWorkspacePrefixes(prefixes)
	defaultBranch := defaultBranchForProject(project, true)
	var files, uncommitted []WorkspaceFileSummary
	var uncommittedErr error
	truncated := false
	compares := make([]workspaceCompareTarget, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(row.WorktreePath) == "" {
			continue
		}
		prefix, ok := prefixes[row.RepoName]
		if !ok {
			continue
		}
		exclude := []string(nil)
		if prefix == "" {
			exclude = childPrefixes
		}
		baseRef := row.BaseRef
		if strings.TrimSpace(baseRef) == "" {
			// Compatibility for sessions created before per-repository base refs
			// were persisted. New rows always use their adapter-resolved ref.
			baseRef = defaultBranch
		}
		resolve := func(rctx context.Context) workspaceCompareTarget {
			return resolveWorkspaceProjectCompare(rctx, row.WorktreePath, row.BaseSHA, baseRef)
		}
		repoFiles, repoTruncated, compare, err := s.workspaceFileSummariesCached(ctx, rec.ID, row.WorktreePath, prefix, exclude, resolve)
		if err != nil {
			return WorkspaceFiles{}, err
		}
		compares = append(compares, compare)
		files, truncated = appendWorkspaceFilesWithCap(files, repoFiles, truncated)
		truncated = truncated || repoTruncated
		if uncommittedErr == nil {
			var repoUncommitted []WorkspaceFileSummary
			repoUncommitted, uncommittedErr = workspaceUncommittedFiles(ctx, row.WorktreePath, prefix, exclude)
			uncommitted = append(uncommitted, repoUncommitted...)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	compare := aggregateWorkspaceCompare(compares)
	result := WorkspaceFiles{
		SessionID:      rec.ID,
		CompareBaseSHA: compare.BaseSHA,
		CompareBaseRef: compare.BaseRef,
		CompareMode:    compare.Mode,
		Files:          files,
		Truncated:      truncated,
	}
	if uncommittedErr != nil {
		if errors.Is(uncommittedErr, ports.ErrWorkspaceRepoUnavailable) {
			return WorkspaceFiles{}, uncommittedErr
		}
		result.Degraded = true
		result.DegradedCode = workspaceDegradedCode(uncommittedErr)
		return result, nil
	}
	sort.Slice(uncommitted, func(i, j int) bool { return uncommitted[i].Path < uncommitted[j].Path })
	result.Sections.Uncommitted = uncommitted
	return result, nil
}

func (s *Service) resolveWorkspaceProjectFileTarget(ctx context.Context, rec domain.SessionRecord, project domain.ProjectRecord, rel string, withCompare bool) (workspaceFileTarget, error) {
	rows, err := s.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return workspaceFileTarget{}, fmt.Errorf("list workspace project rows: %w", err)
	}
	if len(rows) == 0 {
		if !withCompare {
			return workspaceFileTarget{root: rec.Metadata.WorkspacePath, rel: rel}, nil
		}
		prs, err := s.workspaceComparePRs(ctx, rec.ID)
		if err != nil {
			return workspaceFileTarget{}, err
		}
		resolve := func(rctx context.Context) workspaceCompareTarget {
			return resolveWorkspaceCompare(rctx, rec.Metadata.WorkspacePath, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, true), prs)
		}
		compare, changes, err := s.resolveWorkspaceChanges(ctx, rec.ID, rec.Metadata.WorkspacePath, resolve)
		if err != nil {
			return workspaceFileTarget{}, err
		}
		return workspaceFileTarget{root: rec.Metadata.WorkspacePath, rel: rel, compare: compare, changes: changes}, nil
	}
	row, prefix, repoRel, ok := workspaceProjectFileTarget(rec.Metadata.WorkspacePath, rows, rel)
	if !ok {
		return workspaceFileTarget{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	if !withCompare {
		return workspaceFileTarget{root: row.WorktreePath, prefix: prefix, rel: repoRel}, nil
	}
	defaultBranch := defaultBranchForProject(project, true)
	baseRef := row.BaseRef
	if strings.TrimSpace(baseRef) == "" {
		baseRef = defaultBranch
	}
	resolve := func(rctx context.Context) workspaceCompareTarget {
		return resolveWorkspaceProjectCompare(rctx, row.WorktreePath, row.BaseSHA, baseRef)
	}
	compare, changes, err := s.resolveWorkspaceChanges(ctx, rec.ID, row.WorktreePath, resolve)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	return workspaceFileTarget{root: row.WorktreePath, prefix: prefix, rel: repoRel, compare: compare, changes: changes}, nil
}

func workspaceProjectPrefixes(root string, rows []domain.SessionWorktreeRecord) map[string]string {
	prefixes := map[string]string{}
	for _, row := range rows {
		prefix, ok := workspaceProjectPrefix(root, row.WorktreePath)
		if !ok {
			continue
		}
		prefixes[row.RepoName] = prefix
	}
	return prefixes
}

func workspaceProjectPrefix(root, worktree string) (string, bool) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", false
	}
	worktreeAbs, err := filepath.Abs(worktree)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootAbs, worktreeAbs)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return "", true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func nonEmptyWorkspacePrefixes(prefixes map[string]string) []string {
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		if prefix != "" {
			out = append(out, prefix)
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

func workspaceProjectFileTarget(root string, rows []domain.SessionWorktreeRecord, rel string) (domain.SessionWorktreeRecord, string, string, bool) {
	var best domain.SessionWorktreeRecord
	bestPrefix := ""
	bestRel := rel
	found := false
	for _, row := range rows {
		prefix, ok := workspaceProjectPrefix(root, row.WorktreePath)
		if !ok {
			continue
		}
		if prefix == "" {
			if !found {
				best = row
				bestPrefix = ""
				bestRel = rel
				found = true
			}
			continue
		}
		if !strings.HasPrefix(rel, prefix+"/") {
			continue
		}
		if !found || len(prefix) > len(bestPrefix) {
			best = row
			bestPrefix = prefix
			bestRel = strings.TrimPrefix(rel, prefix+"/")
			found = true
		}
	}
	return best, bestPrefix, bestRel, found
}

func workspacePathExcluded(rel string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix == "" {
			continue
		}
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

func joinWorkspaceRelative(prefix, rel string) string {
	if rel == "" {
		return ""
	}
	if prefix == "" {
		return rel
	}
	return prefix + "/" + rel
}
