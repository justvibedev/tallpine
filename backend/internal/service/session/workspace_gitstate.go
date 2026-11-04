package session

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

type workspaceChangeSet struct {
	statuses map[string]WorkspaceFileStatus
	counts   map[string][2]int
	previous map[string]string
	// untracked holds paths git diff's machinery doesn't know about relative
	// to base (status "??" from `git status`, absent from `git diff
	// --name-status`) — computed once here so workspaceFileDiff can tell
	// whether to synthesize a diff without its own extra git spawn.
	untracked map[string]struct{}
}

func (c workspaceChangeSet) changedPaths() map[string]struct{} {
	paths := map[string]struct{}{}
	for rel := range c.statuses {
		paths[rel] = struct{}{}
	}
	for rel := range c.counts {
		paths[rel] = struct{}{}
	}
	return paths
}

func workspaceChangeMaps(ctx context.Context, root, base string) (workspaceChangeSet, error) {
	var (
		diffStatuses, statusStatuses map[string]WorkspaceFileStatus
		diffPrevious, statusPrevious map[string]string
		counts                       map[string][2]int
	)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		if base == "HEAD" {
			// An unborn HEAD diffs against the empty tree instead of failing.
			base = headRevOrEmptyTree(gctx, root)
		}
		diffStatuses, diffPrevious, counts, err = workspaceDiffStats(gctx, root, base)
		return err
	})
	g.Go(func() (err error) {
		statusStatuses, statusPrevious, err = workspaceStatuses(gctx, root)
		return err
	})

	if err := g.Wait(); err != nil {
		return workspaceChangeSet{}, err
	}
	statuses := map[string]WorkspaceFileStatus{}
	previous := map[string]string{}
	for rel, status := range diffStatuses {
		statuses[rel] = status
	}
	for rel, prev := range diffPrevious {
		previous[rel] = prev
	}
	for rel, status := range statusStatuses {
		if _, ok := statuses[rel]; ok {
			continue
		}
		statuses[rel] = status
	}
	for rel, prev := range statusPrevious {
		if _, ok := previous[rel]; ok {
			continue
		}
		previous[rel] = prev
	}
	untracked := map[string]struct{}{}
	for rel, status := range statuses {
		if status != WorkspaceFileAdded {
			continue
		}
		// diffStatuses (git diff --name-status against base) is the correct
		// "does git diff machinery know this path" signal — unlike counts
		// (git diff --numstat), which deliberately omits binary files
		// ("-\t-\tpath") even when they ARE tracked relative to base, so
		// counts membership alone would misclassify a tracked binary Added
		// file as untracked.
		if _, ok := diffStatuses[rel]; ok {
			continue
		}
		untracked[rel] = struct{}{}
		if _, ok := counts[rel]; ok {
			continue
		}
		additions, ok := countUntrackedTextLines(root, rel)
		if ok {
			counts[rel] = [2]int{additions, 0}
		}
	}
	return workspaceChangeSet{statuses: statuses, counts: counts, previous: previous, untracked: untracked}, nil
}

// workspaceGitState computes the git-state sections, the commit list between
// base and HEAD, and ahead/behind counts for one worktree root. The four
// section diffs, the commit log, and the ahead/behind lookup are independent
// git subprocesses and run concurrently.
func workspaceGitState(ctx context.Context, root, base string) (WorkspaceFileSections, workspaceCommits, *int, *int, error) {
	var sections WorkspaceFileSections
	var uncommittedTracked []WorkspaceFileSummary
	var commits workspaceCommits
	var ahead, behind *int
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		uncommittedTracked, err = workspaceUncommittedTracked(gctx, root)
		return err
	})
	g.Go(func() error {
		statuses, previous, counts, err := workspaceDiffStats(gctx, root, "--cached")
		if err != nil {
			return err
		}
		sections.Staged = buildSectionSummaries(root, statuses, counts, previous)
		return nil
	})
	g.Go(func() error {
		statuses, previous, counts, err := workspaceDiffStats(gctx, root)
		if err != nil {
			return err
		}
		sections.Unstaged = buildSectionSummaries(root, statuses, counts, previous)
		return nil
	})
	g.Go(func() error {
		paths, err := gitUntrackedFiles(gctx, root)
		if err != nil {
			return err
		}
		sections.Untracked = buildUntrackedSummaries(root, paths)
		return nil
	})
	if base = strings.TrimSpace(base); base != "" && base != "HEAD" {
		g.Go(func() error {
			statuses, previous, counts, err := workspaceDiffStats(gctx, root, base, "HEAD")
			if err != nil {
				return err
			}
			sections.Committed = buildSectionSummaries(root, statuses, counts, previous)
			return nil
		})
		g.Go(func() (err error) {
			commits.list, commits.truncated, err = gitCommitLog(gctx, root, base)
			return err
		})
	}
	g.Go(func() error {
		ahead, behind = gitAheadBehind(gctx, root)
		return nil
	})
	if err := g.Wait(); err != nil {
		return WorkspaceFileSections{}, workspaceCommits{}, nil, nil, err
	}
	sections.Uncommitted = mergeUncommittedFiles(uncommittedTracked, sections.Untracked)
	return sections, commits, ahead, behind, nil
}

// workspaceUncommittedTracked lists the tracked changes between HEAD and the
// worktree, staged or not, so a partially staged file is one row. HEAD is
// tried first so the common case costs one Git process; an unborn HEAD then
// diffs against the empty tree, which counts a staged new file's later
// worktree edits too.
func workspaceUncommittedTracked(ctx context.Context, root string) ([]WorkspaceFileSummary, error) {
	statuses, previous, counts, err := workspaceDiffStats(ctx, root, "HEAD")
	if err != nil {
		head := headRevOrEmptyTree(ctx, root)
		if head == "HEAD" {
			return nil, err
		}
		statuses, previous, counts, err = workspaceDiffStats(ctx, root, head)
		if err != nil {
			return nil, err
		}
	}
	return buildSectionSummaries(root, statuses, counts, previous), nil
}

// mergeUncommittedFiles combines tracked HEAD..worktree rows with untracked
// rows into one path-sorted Uncommitted section. Git keeps the two sets
// disjoint; should a path appear in both, the tracked row wins, as the
// combined change maps prefer git diff over git status.
func mergeUncommittedFiles(tracked, untracked []WorkspaceFileSummary) []WorkspaceFileSummary {
	files := make([]WorkspaceFileSummary, 0, len(tracked)+len(untracked))
	seen := make(map[string]struct{}, len(tracked))
	for _, file := range tracked {
		seen[file.Path] = struct{}{}
		files = append(files, file)
	}
	for _, file := range untracked {
		if _, ok := seen[file.Path]; !ok {
			files = append(files, file)
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// workspaceUncommittedFiles computes one workspace-project worktree's
// Uncommitted section, with paths carrying the repository's display prefix.
// Workspace projects have no other sections, so this runs its own untracked
// listing instead of sharing workspaceGitSections' one.
func workspaceUncommittedFiles(ctx context.Context, root, prefix string, excludePrefixes []string) ([]WorkspaceFileSummary, error) {
	var tracked, untracked []WorkspaceFileSummary
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() (err error) {
		tracked, err = workspaceUncommittedTracked(gctx, root)
		return err
	})
	g.Go(func() error {
		paths, err := gitUntrackedFiles(gctx, root)
		if err != nil {
			return err
		}
		untracked = buildUntrackedSummaries(root, paths)
		return nil
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}
	merged := mergeUncommittedFiles(tracked, untracked)
	files := make([]WorkspaceFileSummary, 0, len(merged))
	for _, file := range merged {
		if workspacePathExcluded(file.Path, excludePrefixes) {
			continue
		}
		file.Path = joinWorkspaceRelative(prefix, file.Path)
		file.PreviousPath = joinWorkspaceRelative(prefix, file.PreviousPath)
		files = append(files, file)
	}
	return files, nil
}

// workspaceEmptyTrees caches each repository's empty-tree object name, which
// depends only on the repository's immutable hash algorithm.
var workspaceEmptyTrees sync.Map

// workspaceEmptyTree names the empty tree in root's object format (SHA-1 or
// SHA-256). hash-object without -w computes the name and writes nothing; git
// resolves the empty tree without it existing in the object store.
func workspaceEmptyTree(ctx context.Context, root string) (string, bool) {
	if cached, ok := workspaceEmptyTrees.Load(root); ok {
		if tree, ok := cached.(string); ok {
			return tree, true
		}
	}
	out, err := gitWorkspaceOutput(ctx, root, "hash-object", "-t", "tree", os.DevNull)
	if err != nil {
		return "", false
	}
	tree := strings.TrimSpace(out)
	if tree == "" {
		return "", false
	}
	workspaceEmptyTrees.Store(root, tree)
	return tree, true
}

// headRevOrEmptyTree is the "before" side of the uncommitted scope: HEAD, or
// the empty tree while HEAD is unborn so every file in the worktree is an
// addition. It falls back to "HEAD" when neither resolves, which lets the
// caller's own Git command report the failure.
func headRevOrEmptyTree(ctx context.Context, root string) string {
	if _, ok := gitRevision(ctx, root, "HEAD"); ok {
		return "HEAD"
	}
	if tree, ok := workspaceEmptyTree(ctx, root); ok {
		return tree
	}
	return "HEAD"
}

// buildSectionSummaries turns a diff's name-status/numstat maps into sorted
// WorkspaceFileSummary rows.
func buildSectionSummaries(root string, statuses map[string]WorkspaceFileStatus, counts map[string][2]int, previous map[string]string) []WorkspaceFileSummary {
	paths := make([]string, 0, len(statuses))
	for rel := range statuses {
		paths = append(paths, rel)
	}
	sort.Strings(paths)
	files := make([]WorkspaceFileSummary, 0, len(paths))
	for _, rel := range paths {
		status := statuses[rel]
		additions, deletions := counts[rel][0], counts[rel][1]
		size, binary := workspaceFileSizeAndBinary(root, rel, status)
		files = append(files, WorkspaceFileSummary{
			Path:         rel,
			PreviousPath: previous[rel],
			Status:       status,
			Additions:    additions,
			Deletions:    deletions,
			Size:         size,
			Binary:       binary,
		})
	}
	return files
}

// buildUntrackedSummaries turns a set of untracked paths into sorted
// WorkspaceFileSummary rows, counting text lines the same way the base..
// worktree Files list treats an untracked added file.
func buildUntrackedSummaries(root string, paths []string) []WorkspaceFileSummary {
	sorted := append([]string(nil), paths...)
	sort.Strings(sorted)
	files := make([]WorkspaceFileSummary, 0, len(sorted))
	for _, rel := range sorted {
		rel = filepath.ToSlash(rel)
		additions, _ := countUntrackedTextLines(root, rel)
		size, binary := workspaceFileSizeAndBinary(root, rel, WorkspaceFileAdded)
		files = append(files, WorkspaceFileSummary{Path: rel, Status: WorkspaceFileAdded, Additions: additions, Size: size, Binary: binary})
	}
	return files
}

// workspaceSummaryFromFiles aggregates the base..worktree Files list
// (excluding unmodified entries) into panel-header totals.
func workspaceSummaryFromFiles(files []WorkspaceFileSummary) WorkspaceSummary {
	var summary WorkspaceSummary
	for _, file := range files {
		if file.Status == WorkspaceFileUnmodified {
			continue
		}
		summary.Files++
		summary.Additions += file.Additions
		summary.Deletions += file.Deletions
	}
	return summary
}
