package session

import (
	"context"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// WorkspaceFileBlobSide selects which revision of a workspace file to read.
type WorkspaceFileBlobSide string

const (
	// WorkspaceBlobBefore is the file as it exists in the compare base.
	WorkspaceBlobBefore WorkspaceFileBlobSide = "before"
	// WorkspaceBlobAfter is the file as it exists in the session worktree.
	WorkspaceBlobAfter WorkspaceFileBlobSide = "after"
)

// WorkspaceFileBlob is one revision of a workspace file's raw bytes.
type WorkspaceFileBlob struct {
	Path      string
	Side      WorkspaceFileBlobSide
	MediaType string
	Data      []byte
}

// GetWorkspaceFileBlob returns the raw bytes of one side of a workspace file so
// the diff viewer can render an image change. Only image media types are
// served: text content already travels in the file detail response, and the
// diff viewer is this route's only consumer.
func (s *Service) GetWorkspaceFileBlob(ctx context.Context, id domain.SessionID, rawPath string, side WorkspaceFileBlobSide) (WorkspaceFileBlob, error) {
	if side != WorkspaceBlobBefore && side != WorkspaceBlobAfter {
		return WorkspaceFileBlob{}, apierr.Invalid("INVALID_WORKSPACE_BLOB_SIDE", "side must be before or after", nil)
	}
	target, err := s.resolveWorkspaceFileTarget(ctx, id, rawPath)
	if err != nil {
		return WorkspaceFileBlob{}, err
	}
	mediaType := workspaceImageMediaType(target.rel)
	if mediaType == "" {
		return WorkspaceFileBlob{}, apierr.Invalid("UNSUPPORTED_WORKSPACE_BLOB", "workspace blobs are only served for image files", nil)
	}
	blob := WorkspaceFileBlob{
		Path:      joinWorkspaceRelative(target.prefix, target.rel),
		Side:      side,
		MediaType: mediaType,
	}
	status := target.changes.statuses[target.rel]
	if side == WorkspaceBlobAfter {
		if status == WorkspaceFileDeleted {
			return WorkspaceFileBlob{}, apierr.NotFound("WORKSPACE_BLOB_NOT_FOUND", "File does not exist in the session worktree")
		}
		data, err := readWorkspaceFileBytes(target.root, target.rel, maxWorkspaceImageBytes)
		if err != nil {
			return WorkspaceFileBlob{}, err
		}
		blob.Data = data
		return blob, nil
	}
	if target.scratch || status == WorkspaceFileAdded {
		return WorkspaceFileBlob{}, apierr.NotFound("WORKSPACE_BLOB_NOT_FOUND", "File does not exist in the compare base")
	}
	basePath := target.rel
	if status == WorkspaceFileRenamed {
		if previous := target.changes.previous[target.rel]; previous != "" {
			basePath = previous
		}
	}
	// A rename can change the extension, so type the historical bytes by the path
	// they are read from rather than the worktree path. The controller sends
	// nosniff, so a mismatched media type makes the browser refuse to render.
	baseMediaType := workspaceImageMediaType(basePath)
	if baseMediaType == "" {
		return WorkspaceFileBlob{}, apierr.Invalid("UNSUPPORTED_WORKSPACE_BLOB", "workspace blobs are only served for image files", nil)
	}
	blob.MediaType = baseMediaType
	data, err := gitWorkspaceBlob(ctx, target.root, target.compare.gitBase(), basePath, maxWorkspaceImageBytes)
	if err != nil {
		return WorkspaceFileBlob{}, err
	}
	blob.Data = data
	return blob, nil
}

// workspaceImageMediaTypes are the raster formats the diff viewer previews.
// SVG is deliberately absent: it is a text file, so it keeps its line diff.
var workspaceImageMediaTypes = map[string]string{
	".apng": "image/apng",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".gif":  "image/gif",
	".ico":  "image/x-icon",
	".jpeg": "image/jpeg",
	".jpg":  "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

// workspaceImageMediaType reports the image media type for a path, or "" when
// the extension is not a previewable image.
func workspaceImageMediaType(rel string) string {
	return workspaceImageMediaTypes[strings.ToLower(path.Ext(rel))]
}

// workspaceImageDetailMediaType reports the media type the diff viewer should
// preview a file with. A file that still has readable text keeps its line diff,
// so only binary (or deleted, where nothing is left to read) paths qualify.
func workspaceImageDetailMediaType(rel string, binary, deleted bool) string {
	if !binary && !deleted {
		return ""
	}
	return workspaceImageMediaType(rel)
}

// readWorkspaceFileBytes reads a workspace file whole, refusing anything past
// limit instead of buffering it.
func readWorkspaceFileBytes(root, rel string, limit int64) ([]byte, error) {
	handle, info, err := openConfinedWorkspaceFile(root, rel, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer func() { _ = handle.Close() }()
	if info.Size() > limit {
		return nil, apierr.Invalid("WORKSPACE_BLOB_TOO_LARGE", "File is too large to preview", map[string]any{"limitBytes": limit})
	}
	data, err := io.ReadAll(handle)
	if err != nil {
		return nil, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	return data, nil
}

// gitWorkspaceBlob reads one path out of a revision. The object size is asked
// for first so an oversized blob is refused rather than buffered, matching how
// readWorkspaceFileBytes stats the worktree copy before reading it.
func gitWorkspaceBlob(ctx context.Context, root, rev, rel string, limit int64) ([]byte, error) {
	spec := rev + ":" + rel
	sizeOut, err := gitWorkspaceOutput(ctx, root, "cat-file", "-s", spec)
	if err != nil {
		return nil, apierr.NotFound("WORKSPACE_BLOB_NOT_FOUND", "File does not exist in the compare base")
	}
	size, err := strconv.ParseInt(strings.TrimSpace(sizeOut), 10, 64)
	if err != nil {
		return nil, apierr.NotFound("WORKSPACE_BLOB_NOT_FOUND", "File does not exist in the compare base")
	}
	if size > limit {
		return nil, apierr.Invalid("WORKSPACE_BLOB_TOO_LARGE", "File is too large to preview", map[string]any{"limitBytes": limit})
	}
	out, err := gitWorkspaceOutput(ctx, root, "cat-file", "blob", spec)
	if err != nil {
		return nil, apierr.NotFound("WORKSPACE_BLOB_NOT_FOUND", "File does not exist in the compare base")
	}
	return []byte(out), nil
}
