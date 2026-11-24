package browser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// maxUploadFiles matches the desktop's cap (agent-action-page-cdp.ts).
const maxUploadFiles = 32

// uploadRoots are the directories an agent may upload from: its worktree and
// its session temp dir ($TMPDIR), each with symlinks resolved. A root that does
// not exist is left out.
func uploadRoots(session domain.Session, dataDir string) []string {
	var roots []string
	for _, dir := range []string{session.Metadata.WorkspacePath, sessionscratch.TempDir(dataDir, session.ID)} {
		if strings.TrimSpace(dir) == "" || !filepath.IsAbs(dir) {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			roots = append(roots, resolved)
		}
	}
	return roots
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func uploadDenied(format string, args ...any) error {
	return apierr.Invalid("BROWSER_UPLOAD_PATH_DENIED", fmt.Sprintf(format, args...), nil)
}

// resolveUploadPaths checks every requested file is a regular file inside one
// of roots once symlinks are resolved, and returns the resolved paths: the
// desktop receives only those, so a link in the worktree cannot hand the page
// a file from elsewhere.
func resolveUploadPaths(raw any, roots []string) ([]string, error) {
	items, ok := raw.([]any)
	if !ok || len(items) == 0 {
		return nil, apierr.Invalid("INVALID_ARGUMENT", "paths is required", nil)
	}
	if len(items) > maxUploadFiles {
		return nil, apierr.Invalid("INVALID_ARGUMENT", fmt.Sprintf("upload accepts at most %d files", maxUploadFiles), nil)
	}
	if len(roots) == 0 {
		return nil, uploadDenied("this session has no worktree or temp directory to upload from")
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		path, ok := item.(string)
		if !ok || !filepath.IsAbs(path) {
			return nil, apierr.Invalid("INVALID_ARGUMENT", "upload paths must be absolute", nil)
		}
		resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
		if err != nil {
			return nil, uploadDenied("cannot read %s", path)
		}
		inside := false
		for _, root := range roots {
			inside = inside || within(root, resolved)
		}
		if !inside {
			return nil, uploadDenied("%s is outside this session's worktree and $TMPDIR", path)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return nil, uploadDenied("%s is not a regular file", path)
		}
		out = append(out, resolved)
	}
	return out, nil
}
