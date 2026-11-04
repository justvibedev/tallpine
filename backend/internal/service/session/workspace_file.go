package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// GetWorkspaceFile returns one session-worktree file's current text content and
// the git diff for that path. Binary or deleted files omit content. section
// scopes the diff to the git-state section the caller opened the file from
// (see WorkspaceFileSection) since a partially staged file's staged and
// unstaged changes are different diffs.
func (s *Service) GetWorkspaceFile(ctx context.Context, id domain.SessionID, rawPath string, section WorkspaceFileSection) (WorkspaceFileDetail, error) {
	target, err := s.resolveWorkspaceFileTarget(ctx, id, rawPath)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	if target.scratch {
		detail, err := scratchWorkspaceFile(target.root, id, target.rel)
		return finalizeWorkspaceFileDetail(detail), err
	}
	detail, err := workspaceFileDetail(ctx, id, target.root, target.prefix, target.rel, section, target.compare, target.changes)
	return finalizeWorkspaceFileDetail(detail), err
}

// GetWorkspaceFileAtCommit returns the immutable file snapshot and patch for
// one commit that belongs to the session's compare-base..HEAD range.
func (s *Service) GetWorkspaceFileAtCommit(ctx context.Context, id domain.SessionID, rawPath, commitSHA string) (WorkspaceFileDetail, error) {
	current, err := s.ListWorkspaceFiles(ctx, id)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	commit, err := workspaceCommit(current, commitSHA)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	target, err := s.resolveWorkspaceFileTarget(ctx, id, rawPath)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	wantedPath := joinWorkspaceRelative(target.prefix, target.rel)
	var summary *WorkspaceFileSummary
	for i := range commit.Files {
		if commit.Files[i].Path == wantedPath {
			summary = &commit.Files[i]
			break
		}
	}
	if summary == nil {
		return WorkspaceFileDetail{}, apierr.NotFound("WORKSPACE_COMMIT_FILE_NOT_FOUND", "File was not changed by this commit")
	}
	detail, err := workspaceCommitFileDetail(ctx, id, target.root, target.prefix, target.rel, commit.SHA, *summary)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	detail = finalizeWorkspaceFileDetail(detail)
	// A selected commit is historical even when its after-side path still
	// exists in the current worktree. Never present it as directly editable.
	detail.Editable = false
	detail.WorkspaceVersion = current.WorkspaceVersion
	return detail, nil
}

// UpdateWorkspaceFile replaces one existing, bounded UTF-8 workspace file and
// returns its refreshed detail. It deliberately does not create or delete
// paths; those remain agent/editor workflows rather than implicit viewer side
// effects.
func (s *Service) UpdateWorkspaceFile(ctx context.Context, id domain.SessionID, input UpdateWorkspaceFileInput) (WorkspaceFileDetail, error) {
	if input.ExpectedFileFingerprint == "" {
		return WorkspaceFileDetail{}, apierr.Invalid("WORKSPACE_FILE_FINGERPRINT_REQUIRED", "expectedFileFingerprint is required", nil)
	}
	if len(input.Content) > maxWorkspaceFileBytes {
		return WorkspaceFileDetail{}, apierr.Invalid("WORKSPACE_FILE_TOO_LARGE", "File is too large to edit", map[string]any{"limitBytes": maxWorkspaceFileBytes})
	}
	if !utf8.ValidString(input.Content) || isBinary([]byte(input.Content)) {
		return WorkspaceFileDetail{}, apierr.Invalid("WORKSPACE_FILE_BINARY", "Only UTF-8 text files can be edited", nil)
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceFileDetail{}, err
	}

	s.workspaceEditsMu.Lock()
	defer s.workspaceEditsMu.Unlock()

	current, err := s.GetWorkspaceFile(ctx, id, input.Path, "")
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	if current.Deleted {
		return WorkspaceFileDetail{}, apierr.Conflict("WORKSPACE_FILE_DELETED", "Deleted files cannot be edited", nil)
	}
	if current.Binary || current.ContentTruncated {
		return WorkspaceFileDetail{}, apierr.Invalid("WORKSPACE_FILE_NOT_EDITABLE", "Only complete text files can be edited", nil)
	}
	if current.FileFingerprint != input.ExpectedFileFingerprint {
		return WorkspaceFileDetail{}, apierr.Conflict("WORKSPACE_FILE_STALE", "File changed while it was being edited", map[string]any{"fileFingerprint": current.FileFingerprint})
	}

	target, err := s.resolveWorkspaceFileTarget(ctx, id, input.Path)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	handle, _, err := openConfinedWorkspaceFile(target.root, target.rel, os.O_RDWR)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	defer func() { _ = handle.Close() }()
	latest, err := io.ReadAll(handle)
	if err != nil {
		return WorkspaceFileDetail{}, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	if string(latest) != current.Content {
		return WorkspaceFileDetail{}, apierr.Conflict("WORKSPACE_FILE_STALE", "File changed while it was being edited", nil)
	}
	if err := handle.Truncate(0); err != nil {
		return WorkspaceFileDetail{}, fmt.Errorf("write workspace file: %w", err)
	}
	if _, err := handle.WriteAt([]byte(input.Content), 0); err != nil {
		return WorkspaceFileDetail{}, fmt.Errorf("write workspace file: %w", err)
	}
	s.InvalidateWorkspaceCache(id)
	return s.GetWorkspaceFile(ctx, id, input.Path, "")
}

func workspaceFileEditable(size int64, binary, deleted bool) bool {
	return !binary && !deleted && size <= maxWorkspaceFileBytes
}

// workspaceFileTarget is one resolved workspace path: the worktree that owns
// it, the display prefix the path carries in a workspace project, and the
// compare base and change set that worktree is diffed against.
type workspaceFileTarget struct {
	root    string
	prefix  string
	rel     string
	compare workspaceCompareTarget
	changes workspaceChangeSet
	scratch bool
}

// resolveWorkspaceFileTarget maps a request path onto the worktree that owns it
// and that worktree's compare state. Shared by the file detail read model and
// the raw blob route so both resolve a path the same way.
func (s *Service) resolveWorkspaceFileTarget(ctx context.Context, id domain.SessionID, rawPath string) (workspaceFileTarget, error) {
	return s.resolveWorkspaceFileTargetWithCompare(ctx, id, rawPath, true)
}

// Plain working-tree reads need path confinement, not a repository-wide Git scan.
func (s *Service) resolveWorkspaceFileTargetWithCompare(ctx context.Context, id domain.SessionID, rawPath string, withCompare bool) (workspaceFileTarget, error) {
	rec, err := s.sessionWorkspaceRecord(ctx, id)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	rel, err := cleanWorkspaceRelativePath(rawPath)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	project, projectOK, err := s.sessionProject(ctx, rec)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	projectKind := domain.ProjectKindSingleRepo
	if isStandaloneScratchWorkspace(rec) {
		projectKind = domain.ProjectKindScratch
	}
	if projectOK {
		projectKind = project.Kind.WithDefault()
	}
	if projectKind == domain.ProjectKindScratch {
		return workspaceFileTarget{root: rec.Metadata.WorkspacePath, rel: rel, scratch: true}, nil
	}
	if projectKind == domain.ProjectKindWorkspace {
		return s.resolveWorkspaceProjectFileTarget(ctx, rec, project, rel, withCompare)
	}
	if !withCompare {
		return workspaceFileTarget{root: rec.Metadata.WorkspacePath, rel: rel}, nil
	}
	prs, err := s.workspaceComparePRs(ctx, rec.ID)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	resolve := func(rctx context.Context) workspaceCompareTarget {
		return resolveWorkspaceCompare(rctx, rec.Metadata.WorkspacePath, rec.Metadata.DiffBaseSHA, rec.Metadata.DiffBaseRef, defaultBranchForProject(project, projectOK), prs)
	}
	compare, changes, err := s.resolveWorkspaceChanges(ctx, id, rec.Metadata.WorkspacePath, resolve)
	if err != nil {
		return workspaceFileTarget{}, err
	}
	return workspaceFileTarget{root: rec.Metadata.WorkspacePath, rel: rel, compare: compare, changes: changes}, nil
}

func workspaceFileDetail(ctx context.Context, id domain.SessionID, root, prefix, rel string, section WorkspaceFileSection, compare workspaceCompareTarget, changes workspaceChangeSet) (WorkspaceFileDetail, error) {
	status := changes.statuses[rel]
	if status == "" {
		status = WorkspaceFileUnmodified
	}
	additions, deletions := changes.counts[rel][0], changes.counts[rel][1]
	previousPath := changes.previous[rel]
	detail := WorkspaceFileDetail{
		SessionID:      id,
		Path:           joinWorkspaceRelative(prefix, rel),
		PreviousPath:   joinWorkspaceRelative(prefix, previousPath),
		Status:         status,
		Additions:      additions,
		Deletions:      deletions,
		Deleted:        status == WorkspaceFileDeleted,
		CompareBaseSHA: compare.BaseSHA,
		CompareBaseRef: compare.BaseRef,
		CompareMode:    compare.Mode,
	}
	if !detail.Deleted {
		handle, info, err := openConfinedWorkspaceFile(root, rel, os.O_RDONLY)
		if err != nil {
			return WorkspaceFileDetail{}, err
		}
		defer func() { _ = handle.Close() }()
		detail.Size = info.Size()
		detail.fileModTime = info.ModTime()
		content, binary, truncated, err := readWorkspaceTextHandle(handle, maxWorkspaceFileBytes)
		if err != nil {
			return WorkspaceFileDetail{}, err
		}
		detail.Binary = binary
		detail.ContentTruncated = truncated
		if !binary {
			detail.Content = content
		}
	}
	detail.ImageMediaType = workspaceImageDetailMediaType(rel, detail.Binary, detail.Deleted)
	_, untracked := changes.untracked[rel]
	diff, truncated, err := workspaceFileDiff(ctx, root, compare.gitBase(), rel, status, section, previousPath, detail.Content, detail.Binary, untracked)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	detail.Diff = diff
	detail.DiffTruncated = truncated
	return detail, nil
}

func workspaceCommitFileDetail(ctx context.Context, id domain.SessionID, root, prefix, rel, commitSHA string, summary WorkspaceFileSummary) (WorkspaceFileDetail, error) {
	detail := WorkspaceFileDetail{
		SessionID:      id,
		Path:           joinWorkspaceRelative(prefix, rel),
		PreviousPath:   joinWorkspaceRelative(prefix, summary.PreviousPath),
		Status:         summary.Status,
		Additions:      summary.Additions,
		Deletions:      summary.Deletions,
		Deleted:        summary.Status == WorkspaceFileDeleted,
		CompareBaseSHA: commitSHA + "^",
		CompareBaseRef: commitSHA,
		CompareMode:    WorkspaceCompareBase,
		Historical:     true,
	}
	if !detail.Deleted {
		data, size, exists, truncated, err := readGitRevision(ctx, root, commitSHA+":"+rel)
		if err != nil {
			return WorkspaceFileDetail{}, err
		}
		if !exists {
			return WorkspaceFileDetail{}, apierr.NotFound("WORKSPACE_COMMIT_FILE_NOT_FOUND", "File does not exist in this commit")
		}
		detail.Size = size
		detail.ContentTruncated = truncated || size > maxWorkspaceFileBytes
		if !truncated {
			detail.Binary = isBinary(data) || !utf8.Valid(data)
			if !detail.Binary && !detail.ContentTruncated {
				detail.Content = string(data)
			}
		}
	}
	detail.ImageMediaType = workspaceImageDetailMediaType(rel, detail.Binary, detail.Deleted)
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--unified=3", commitSHA + "^", commitSHA, "--"}
	if summary.Status == WorkspaceFileRenamed && summary.PreviousPath != "" {
		args = append(args, summary.PreviousPath)
	}
	args = append(args, rel)
	out, err := gitWorkspaceOutput(ctx, root, args...)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	detail.Diff, detail.DiffTruncated = truncateUTF8(out, maxWorkspaceDiffBytes)
	return detail, nil
}

func workspaceFileSizeAndBinary(root, rel string, status WorkspaceFileStatus) (int64, bool) {
	if status == WorkspaceFileDeleted {
		return 0, false
	}
	handle, info, err := openConfinedWorkspaceFile(root, rel, os.O_RDONLY)
	if err != nil {
		return 0, false
	}
	defer func() { _ = handle.Close() }()
	_, binary, _, err := readWorkspaceTextHandle(handle, 8192)
	if err != nil {
		return info.Size(), false
	}
	return info.Size(), binary
}

func workspaceFileDiff(ctx context.Context, root, base, rel string, status WorkspaceFileStatus, section WorkspaceFileSection, previousPath, content string, binary, untracked bool) (string, bool, error) {
	if status == WorkspaceFileUnmodified {
		return "", false, nil
	}
	if status == WorkspaceFileAdded && untracked {
		if binary {
			return "", false, nil
		}
		diff := syntheticAddedFileDiff(rel, content)
		truncatedDiff, truncated := truncateUTF8(diff, maxWorkspaceDiffBytes)
		return truncatedDiff, truncated, nil
	}
	// staged mirrors the `--cached` diff sections.Staged is built from (index
	// vs HEAD); unstaged mirrors the bare diff sections.Unstaged is built from
	// (worktree vs index); uncommitted mirrors sections.Uncommitted (worktree
	// vs HEAD). Anything else falls back to base..worktree, the combined
	// change shown before per-section diffs existed.
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--unified=3"}
	switch section {
	case WorkspaceFileSectionUncommitted:
		args = append(args, headRevOrEmptyTree(ctx, root))
	case WorkspaceFileSectionStaged:
		args = append(args, "--cached")
	case WorkspaceFileSectionUnstaged:
		// No revision arg: worktree vs index.
	default:
		args = append(args, base)
	}
	args = append(args, "--")
	if status == WorkspaceFileRenamed && previousPath != "" {
		args = append(args, previousPath)
	}
	args = append(args, rel)
	out, err := gitWorkspaceOutput(ctx, root, args...)
	if err != nil {
		return "", false, err
	}
	truncatedDiff, truncated := truncateUTF8(out, maxWorkspaceDiffBytes)
	return truncatedDiff, truncated, nil
}

func syntheticAddedFileDiff(rel, content string) string {
	lines := strings.SplitAfter(content, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", rel, rel)
	b.WriteString("new file mode 100644\n")
	b.WriteString("--- /dev/null\n")
	fmt.Fprintf(&b, "+++ b/%s\n", rel)
	fmt.Fprintf(&b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		b.WriteByte('+')
		b.WriteString(line)
		if !strings.HasSuffix(line, "\n") {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
