package session

import (
	"os"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// afterWorkspaceFileCheck lets tests swap the path between check and open.
var afterWorkspaceFileCheck func()

// openConfinedWorkspaceFile validates rel like confinedWorkspaceFile, then
// opens it through an os.Root so a symlink swapped in after the check cannot
// lead outside the workspace, and stats the open handle (not the name) so the
// returned info describes the file actually read or written. The caller closes
// the handle.
func openConfinedWorkspaceFile(root, rel string, flag int) (*os.File, os.FileInfo, error) {
	resolved, err := confinedWorkspaceFile(root, rel)
	if err != nil {
		return nil, nil, err
	}
	if afterWorkspaceFileCheck != nil {
		afterWorkspaceFileCheck()
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	rootResolved, err := resolvedFilesystemPath(rootAbs)
	if err != nil {
		return nil, nil, apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	within, err := filepath.Rel(rootResolved, resolved)
	if err != nil {
		return nil, nil, apierr.Invalid("INVALID_WORKSPACE_PATH", "path escapes session workspace", nil)
	}
	scope, err := os.OpenRoot(rootResolved)
	if err != nil {
		return nil, nil, apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	defer func() { _ = scope.Close() }()
	handle, err := scope.OpenFile(within, flag, 0)
	if err != nil {
		return nil, nil, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	info, err := handle.Stat()
	if err != nil {
		_ = handle.Close()
		return nil, nil, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	if info.IsDir() {
		_ = handle.Close()
		return nil, nil, apierr.Invalid("WORKSPACE_PATH_IS_DIRECTORY", "Workspace path is a directory", nil)
	}
	return handle, info, nil
}
