package session

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/hookutil"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

func scratchWorkspaceFiles(root string) ([]WorkspaceFileSummary, bool, error) {
	rootResolved, err := resolvedWorkspaceRoot(root)
	if err != nil {
		return nil, false, err
	}
	managedByDir := make(map[string]map[string]struct{})
	var files []WorkspaceFileSummary
	truncated := false
	err = filepath.WalkDir(rootResolved, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fullPath == rootResolved {
			return nil
		}
		relPath, err := filepath.Rel(rootResolved, fullPath)
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(relPath)
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		dirPath := filepath.Dir(fullPath)
		managed, ok := managedByDir[dirPath]
		if !ok {
			managed = scratchTallpineManagedNamesIn(dirPath)
			managedByDir[dirPath] = managed
		}
		if _, hidden := managed[entry.Name()]; hidden {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if scratchTallpineManagedCopilotProfile(path.Dir(rel), entry.Name(), fullPath) {
			return nil
		}
		info, include, err := scratchWorkspaceFileInfo(rootResolved, fullPath, entry)
		if err != nil {
			return err
		}
		if !include {
			return nil
		}
		if len(files) >= maxWorkspaceFiles {
			truncated = true
			return filepath.SkipAll
		}
		_, binary, err := readWorkspaceTextFile(fullPath, 8192)
		if err != nil {
			return err
		}
		files = append(files, WorkspaceFileSummary{
			Path:      rel,
			Status:    WorkspaceFileAdded,
			Additions: 0,
			Deletions: 0,
			Size:      info.Size(),
			Binary:    binary,
		})
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, truncated, nil
}

// scratchTallpineManagedNamesIn returns the basenames claimed by a Tallpine-managed
// .gitignore in dir. Adapter-owned patterns are anchored to that directory and
// name files only. Missing, malformed, or unreadable markers safely reveal the
// files instead of failing an otherwise readable workspace-tree request.
func scratchTallpineManagedNamesIn(dir string) map[string]struct{} {
	managed := make(map[string]struct{})
	marker := filepath.Join(dir, ".gitignore")
	info, err := os.Lstat(marker)
	if err != nil || !info.Mode().IsRegular() {
		return managed
	}
	content, binary, err := readWorkspaceTextFile(marker, 64*1024)
	if err != nil || binary || !hookutil.HasGitignoreSentinel(content) {
		return managed
	}
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
			continue
		}
		name := path.Clean(strings.TrimPrefix(line, "/"))
		if name == "." || name == ".." || strings.Contains(name, "/") {
			continue
		}
		managed[name] = struct{}{}
	}
	return managed
}

func scratchTallpineManagedCopilotProfile(dir, name, fullPath string) bool {
	if dir != ".github/agents" || (!strings.HasPrefix(name, "tallpine-") && !strings.HasPrefix(name, "ao-")) || !strings.HasSuffix(name, ".agent.md") {
		return false
	}
	info, err := os.Lstat(fullPath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	content, binary, err := readWorkspaceTextFile(fullPath, 64*1024)
	return err == nil && !binary && hookutil.HasCopilotAgentProfileSentinel(content)
}

// scratchDirectoryOnlyTallpineManaged reports whether a directory contains control
// files but no user-visible file. This keeps an otherwise empty .kimi,
// .claude, or similar adapter directory from surviving as a misleading folder
// in the Files tree after its contents have been filtered.
func scratchDirectoryOnlyTallpineManaged(rootResolved, rel string) bool {
	// Tallpine workspace metadata lives under hidden roots. Avoid recursively probing
	// ordinary scratch directories just to prove that they are user-visible.
	rootName := strings.SplitN(rel, "/", 2)[0]
	if !strings.HasPrefix(rootName, ".") {
		return false
	}
	hasManaged := false
	hasVisible := false
	visited := 0
	managedByDir := make(map[string]map[string]struct{})
	target := filepath.Join(rootResolved, filepath.FromSlash(rel))
	err := filepath.WalkDir(target, func(fullPath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fullPath != target {
			visited++
			if visited > maxWorkspaceFiles {
				hasVisible = true
				return filepath.SkipAll
			}
		}
		if entry.IsDir() {
			if entry.Name() == ".git" && fullPath != target {
				return filepath.SkipDir
			}
			return nil
		}
		pathRel, err := filepath.Rel(rootResolved, fullPath)
		if err != nil {
			return err
		}
		workspaceRel := filepath.ToSlash(pathRel)
		dirPath := filepath.Dir(fullPath)
		managed, ok := managedByDir[dirPath]
		if !ok {
			managed = scratchTallpineManagedNamesIn(dirPath)
			managedByDir[dirPath] = managed
		}
		if _, hidden := managed[entry.Name()]; hidden {
			hasManaged = true
			return nil
		}
		if scratchTallpineManagedCopilotProfile(path.Dir(workspaceRel), entry.Name(), fullPath) {
			hasManaged = true
			return nil
		}
		_, include, err := scratchWorkspaceFileInfo(rootResolved, fullPath, entry)
		if err != nil {
			return err
		}
		if include {
			hasVisible = true
			return filepath.SkipAll
		}
		return nil
	})
	return err == nil && hasManaged && !hasVisible
}

func isStandaloneScratchWorkspace(rec domain.SessionRecord) bool {
	return rec.IsStandalone() && rec.Kind == domain.KindWorker
}

func scratchWorkspaceFile(root string, id domain.SessionID, rel string) (WorkspaceFileDetail, error) {
	handle, info, err := openConfinedWorkspaceFile(root, rel, os.O_RDONLY)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	defer func() { _ = handle.Close() }()
	content, binary, truncated, err := readWorkspaceTextHandle(handle, maxWorkspaceFileBytes)
	if err != nil {
		return WorkspaceFileDetail{}, err
	}
	additions := 0
	if !binary {
		additions = textLineCount(content)
	}
	detail := WorkspaceFileDetail{
		SessionID:        id,
		Path:             rel,
		Status:           WorkspaceFileAdded,
		Additions:        additions,
		Deletions:        0,
		Size:             info.Size(),
		Binary:           binary,
		ContentTruncated: truncated,
		ImageMediaType:   workspaceImageDetailMediaType(rel, binary, false),
		fileModTime:      info.ModTime(),
	}
	if !binary {
		detail.Content = content
	}
	return detail, nil
}

func resolvedWorkspaceRoot(root string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	rootResolved, err := resolvedFilesystemPath(rootAbs)
	if err != nil {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	info, err := os.Stat(rootResolved)
	if err != nil || !info.IsDir() {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	return rootResolved, nil
}

func scratchWorkspaceFileInfo(rootResolved, fullPath string, entry fs.DirEntry) (os.FileInfo, bool, error) {
	info, err := entry.Info()
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if !info.Mode().IsRegular() {
			return nil, false, nil
		}
		abs, err := filepath.Abs(fullPath)
		if err != nil {
			return nil, false, err
		}
		if !pathx.StrictlyWithin(rootResolved, abs) {
			return nil, false, nil
		}
		return info, true, nil
	}
	targetInfo, ok := statScratchFileTarget(fullPath)
	if !ok {
		return nil, false, nil
	}
	resolved, ok := resolvedScratchPath(fullPath)
	if !ok {
		return nil, false, nil
	}
	if !pathx.StrictlyWithin(rootResolved, resolved) {
		return nil, false, nil
	}
	return targetInfo, true, nil
}

func statScratchFileTarget(filePath string) (os.FileInfo, bool) {
	info, err := os.Stat(filePath)
	return info, err == nil && !info.IsDir()
}

func resolvedScratchPath(filePath string) (string, bool) {
	resolved, err := resolvedFilesystemPath(filePath)
	return resolved, err == nil
}
