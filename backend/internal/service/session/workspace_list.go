package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/sync/errgroup"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// WorkspaceWatchPaths returns every worktree that contributes files to a
// session workspace read model. Workspace projects include child repositories
// that are deliberately ignored by the parent Git repository.
func (s *Service) WorkspaceWatchPaths(ctx context.Context, id domain.SessionID) ([]string, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return nil, err
	}
	paths := []string{rec.Metadata.WorkspacePath}
	seen := map[string]struct{}{filepath.Clean(rec.Metadata.WorkspacePath): {}}
	rows, err := s.store.ListSessionWorktrees(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("list workspace project rows: %w", err)
	}
	for _, row := range rows {
		workspacePath := strings.TrimSpace(row.WorktreePath)
		if workspacePath == "" {
			continue
		}
		workspacePath = filepath.Clean(workspacePath)
		if _, ok := seen[workspacePath]; ok {
			continue
		}
		info, statErr := os.Stat(workspacePath)
		if statErr != nil || !info.IsDir() {
			continue
		}
		seen[workspacePath] = struct{}{}
		paths = append(paths, workspacePath)
	}
	return paths, nil
}

// InvalidateWorkspaceCache purges cached compare-base and git-status data for
// a session when the workspace SSE filesystem watcher observes a change.
// Callers sharing an in-flight load may still receive its uncached snapshot;
// the first request after that load completes recomputes the data.
func (s *Service) InvalidateWorkspaceCache(id domain.SessionID) {
	s.workspaceCache.invalidateSession(id)
	if s.workspaceManifests.markStale(id) {
		s.refreshWorkspaceManifestInBackground(id)
	}
}

// ListWorkspaceFiles returns all tracked and untracked, non-ignored files in a
// session worktree, annotated with their current git status against the
// session's recorded base when available.
func (s *Service) ListWorkspaceFiles(ctx context.Context, id domain.SessionID) (WorkspaceFiles, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return WorkspaceFiles{}, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return WorkspaceFiles{}, err
	}
	projectKind := domain.ProjectKindSingleRepo
	if isStandaloneScratchWorkspace(rec) {
		projectKind = domain.ProjectKindScratch
	}
	if projectOK {
		projectKind = project.Kind.WithDefault()
	}
	if projectKind == domain.ProjectKindScratch {
		files, truncated, err := scratchWorkspaceFiles(rec.Metadata.WorkspacePath)
		if err != nil {
			return WorkspaceFiles{}, err
		}
		// Every scratch file is uncommitted: there is no Git history to compare.
		return finalizeWorkspaceFiles(WorkspaceFiles{SessionID: id, Files: files, Sections: WorkspaceFileSections{Uncommitted: slices.Clone(files)}, Truncated: truncated}), nil
	}
	if projectKind == domain.ProjectKindWorkspace {
		result, err := s.listWorkspaceProjectFiles(ctx, rec, project)
		if err != nil {
			return WorkspaceFiles{}, err
		}
		return finalizeWorkspaceFiles(result), nil
	}
	prs, err := s.workspaceComparePRs(ctx, rec.ID)
	if err != nil {
		return WorkspaceFiles{}, err
	}
	resolve := func(rctx context.Context) workspaceCompareTarget {
		return resolveWorkspaceCompare(rctx, rec.Metadata.WorkspacePath, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, projectOK), prs)
	}
	files, truncated, compare, err := s.workspaceFileSummariesCached(ctx, id, rec.Metadata.WorkspacePath, "", nil, resolve)
	if err != nil {
		return WorkspaceFiles{}, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	sections, commits, ahead, behind, err := workspaceGitState(ctx, rec.Metadata.WorkspacePath, compare.gitBase())
	if err != nil {
		if errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
			return WorkspaceFiles{}, err
		}
		return finalizeWorkspaceFiles(WorkspaceFiles{
			SessionID:      id,
			CompareBaseSHA: compare.BaseSHA,
			CompareBaseRef: compare.BaseRef,
			CompareMode:    compare.Mode,
			Files:          files,
			Truncated:      truncated,
			Summary:        workspaceSummaryFromFiles(files),
			Degraded:       true,
			DegradedCode:   workspaceDegradedCode(err),
		}), nil
	}
	return finalizeWorkspaceFiles(WorkspaceFiles{
		SessionID:        id,
		CompareBaseSHA:   compare.BaseSHA,
		CompareBaseRef:   compare.BaseRef,
		CompareMode:      compare.Mode,
		Files:            files,
		Truncated:        truncated,
		Sections:         sections,
		Commits:          commits.list,
		CommitsTruncated: commits.truncated,
		Summary:          workspaceSummaryFromFiles(files),
		Ahead:            ahead,
		Behind:           behind,
	}), nil
}

func appendWorkspaceFilesWithCap(files, repoFiles []WorkspaceFileSummary, truncated bool) ([]WorkspaceFileSummary, bool) {
	remaining := maxWorkspaceFiles - len(files)
	if remaining <= 0 {
		if len(repoFiles) > 0 {
			truncated = true
		}
		return files, truncated
	}
	if len(repoFiles) > remaining {
		files = append(files, repoFiles[:remaining]...)
		return files, true
	}
	return append(files, repoFiles...), truncated
}

// workspaceFileSummariesCached resolves the changed-file list for one
// worktree root, reusing a cached compare+status result when a recent
// ListWorkspaceFiles/GetWorkspaceFile call for the same session/root already
// computed it (see workspaceCache). On a cache miss it resolves the compare
// base, computes git status/diff/numstat, and lists working-tree files
// concurrently, since none of those git calls depend on each other.
func (s *Service) workspaceFileSummariesCached(
	ctx context.Context, id domain.SessionID, root, prefix string, excludePrefixes []string,
	resolve func(ctx context.Context) workspaceCompareTarget,
) ([]WorkspaceFileSummary, bool, workspaceCompareTarget, error) {
	var compare workspaceCompareTarget
	var changes workspaceChangeSet
	var lsParts []string
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		compare, changes, err = s.resolveWorkspaceChanges(gctx, id, root, resolve)
		return err
	})
	g.Go(func() (err error) {
		lsParts, err = s.workspacePaths(gctx, id, root)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, false, workspaceCompareTarget{}, err
	}
	paths, truncated := mergeWorkspaceFilePaths(lsParts, changes.changedPaths())
	files := buildWorkspaceFileSummaries(root, prefix, excludePrefixes, paths, changes)
	return files, truncated, compare, nil
}

func buildWorkspaceFileSummaries(root, prefix string, excludePrefixes, paths []string, changes workspaceChangeSet) []WorkspaceFileSummary {
	files := make([]WorkspaceFileSummary, 0, len(paths))
	for _, rel := range paths {
		if workspacePathExcluded(rel, excludePrefixes) {
			continue
		}
		status := changes.statuses[rel]
		if status == "" {
			status = WorkspaceFileUnmodified
		}
		additions, deletions := changes.counts[rel][0], changes.counts[rel][1]
		size, binary := workspaceFileSizeAndBinary(root, rel, status)
		files = append(files, WorkspaceFileSummary{
			Path:         joinWorkspaceRelative(prefix, rel),
			PreviousPath: joinWorkspaceRelative(prefix, changes.previous[rel]),
			Status:       status,
			Additions:    additions,
			Deletions:    deletions,
			Size:         size,
			Binary:       binary,
		})
	}
	return files
}

// resolveWorkspaceChanges resolves the compare base and git status/diff maps
// for one worktree root, reusing a cached result from a recent list/detail
// call for the same session/root instead of re-running resolve+
// workspaceChangeMaps from scratch. On a cache miss, concurrent callers for
// the same (session, root) — e.g. "Expand All" firing GetWorkspaceFile for
// every changed file in the same instant — share a single in-flight
// resolve+workspaceChangeMaps call via singleflight instead of each spawning
// its own redundant set of git subprocesses.
func (s *Service) resolveWorkspaceChanges(
	ctx context.Context, id domain.SessionID, root string,
	resolve func(ctx context.Context) workspaceCompareTarget,
) (workspaceCompareTarget, workspaceChangeSet, error) {
	key := workspaceCacheKey{session: id, root: root}
	if cached, ok := s.workspaceCache.get(key); ok {
		return cached.compare, cached.changes, nil
	}
	v, err, _ := s.workspaceGroup.Do(key.String(), func() (any, error) {
		// A concurrent caller may have already populated the cache while this
		// call was queued behind the singleflight lock — re-check before
		// doing the work again.
		if cached, ok := s.workspaceCache.get(key); ok {
			return cached, nil
		}
		entry := s.workspaceCache.beginLoad(key)
		defer s.workspaceCache.finishLoad(key, entry, false)
		compare := resolve(ctx)
		changes, err := workspaceChangeMaps(ctx, root, compare.gitBase())
		if err != nil {
			return nil, err
		}
		*entry = workspaceCacheEntry{at: s.now(), compare: compare, changes: changes}
		s.workspaceCache.finishLoad(key, entry, true)
		return *entry, nil
	})
	if err != nil {
		return workspaceCompareTarget{}, workspaceChangeSet{}, err
	}
	entry, ok := v.(workspaceCacheEntry)
	if !ok {
		return workspaceCompareTarget{}, workspaceChangeSet{}, fmt.Errorf("resolveWorkspaceChanges: unexpected singleflight result type %T", v)
	}
	return entry.compare, entry.changes, nil
}

// gitLsFilesParts runs the `git ls-files` subprocess, kept separate from the
// (pure, no I/O) merge step in mergeWorkspaceFilePaths so callers can run it
// concurrently with other independent git calls (see workspaceFileSummariesCached).
func gitLsFilesParts(ctx context.Context, root string) ([]string, error) {
	out, err := gitWorkspaceOutput(ctx, root, "ls-files", "-z", "-co", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

func mergeWorkspaceFilePaths(parts []string, extraPaths map[string]struct{}) ([]string, bool) {
	paths := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	truncated := false
	addPath := func(part string) {
		rel := filepath.ToSlash(part)
		if rel == "" {
			return
		}
		if _, ok := seen[rel]; ok {
			return
		}
		seen[rel] = struct{}{}
		if len(paths) >= maxWorkspaceFiles {
			truncated = true
			return
		}
		paths = append(paths, rel)
	}
	extra := make([]string, 0, len(extraPaths))
	for rel := range extraPaths {
		extra = append(extra, rel)
	}
	sort.Strings(extra)
	for _, rel := range extra {
		addPath(rel)
	}
	for _, part := range parts {
		addPath(part)
	}
	return paths, truncated
}
