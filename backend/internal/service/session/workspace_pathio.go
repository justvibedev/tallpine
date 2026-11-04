package session

import (
	"bytes"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
	"tallpine.eu/tallpine/backend/internal/pathx"
)

func confinedWorkspaceFile(root, rel string) (string, error) {
	clean, err := cleanWorkspaceRelativePath(rel)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	rootResolved, err := resolvedFilesystemPath(rootAbs)
	if err != nil {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	rootInfo, err := os.Stat(rootResolved)
	if err != nil || !rootInfo.IsDir() {
		return "", apierr.NotFound("SESSION_WORKSPACE_NOT_FOUND", "Session workspace not found")
	}
	target := filepath.Join(rootResolved, filepath.FromSlash(clean))
	targetAbs, err := filepath.Abs(target)
	if err != nil || !pathx.StrictlyWithin(rootResolved, targetAbs) {
		return "", apierr.Invalid("INVALID_WORKSPACE_PATH", "path escapes session workspace", nil)
	}
	resolved := rootResolved
	for _, part := range strings.Split(clean, "/") {
		resolved = filepath.Join(resolved, part)
		resolved, err = resolvedFilesystemPath(resolved)
		if err != nil {
			return "", apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
		}
		if !pathx.StrictlyWithin(rootResolved, resolved) {
			return "", apierr.Invalid("INVALID_WORKSPACE_PATH", "path escapes session workspace", nil)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	if info.IsDir() {
		return "", apierr.Invalid("WORKSPACE_PATH_IS_DIRECTORY", "Workspace path is a directory", nil)
	}
	return resolved, nil
}

func cleanWorkspaceRelativePath(raw string) (string, error) {
	// Backslash is a path separator only on Windows. Everywhere else it is a
	// legal filename character, so folding it here makes a file named `a\b.txt`
	// unopenable — and worse, silently resolves it to `a/b.txt`, a different
	// file that may well exist.
	normalized := raw
	if runtime.GOOS == "windows" {
		normalized = strings.ReplaceAll(raw, "\\", "/")
	}
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" || path.IsAbs(trimmed) || filepath.IsAbs(raw) || filepath.VolumeName(raw) != "" {
		return "", apierr.Invalid("INVALID_WORKSPACE_PATH", "workspace path must be relative", nil)
	}
	clean := path.Clean(trimmed)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", apierr.Invalid("INVALID_WORKSPACE_PATH", "path escapes session workspace", nil)
	}
	return clean, nil
}

func readWorkspaceTextFile(file string, limit int) (string, bool, error) {
	handle, err := os.Open(file)
	if err != nil {
		return "", false, apierr.NotFound("WORKSPACE_FILE_NOT_FOUND", "Workspace file not found")
	}
	defer func() { _ = handle.Close() }()
	content, binary, _, err := readWorkspaceTextHandle(handle, limit)
	return content, binary, err
}

func readWorkspaceTextHandle(handle io.Reader, limit int) (string, bool, bool, error) {
	data, err := io.ReadAll(io.LimitReader(handle, int64(limit+1)))
	if err != nil {
		return "", false, false, err
	}
	truncated := len(data) > limit
	if truncated {
		data = data[:limit]
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", true, truncated, nil
	}
	if !utf8.Valid(data) && truncated {
		// A bounded preview can end in the middle of a valid multi-byte rune.
		// Remove only that possible partial suffix; invalid UTF-8 elsewhere is
		// still classified as binary below.
		for trim := 1; trim < utf8.UTFMax && trim < len(data); trim++ {
			if candidate := data[:len(data)-trim]; utf8.Valid(candidate) {
				data = candidate
				break
			}
		}
	}
	if !utf8.Valid(data) {
		return "", true, truncated, nil
	}
	return string(data), false, truncated, nil
}

func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data)
}

func countUntrackedTextLines(root, rel string) (int, bool) {
	handle, _, err := openConfinedWorkspaceFile(root, rel, os.O_RDONLY)
	if err != nil {
		return 0, false
	}
	defer func() { _ = handle.Close() }()
	content, binary, _, err := readWorkspaceTextHandle(handle, maxWorkspaceFileBytes)
	if err != nil || binary {
		return 0, false
	}
	if content == "" {
		return 0, true
	}
	return textLineCount(content), true
}

func textLineCount(content string) int {
	if content == "" {
		return 0
	}
	return strings.Count(content, "\n") + btoi(!strings.HasSuffix(content, "\n"))
}

func truncateUTF8(in string, limit int) (string, bool) {
	if len(in) <= limit {
		return in, false
	}
	end := 0
	for i := range in {
		if i > limit {
			break
		}
		end = i
	}
	if end == 0 {
		end = limit
	}
	return in[:end], true
}

func splitNUL(out string) []string {
	raw := strings.TrimRight(out, "\x00")
	if raw == "" {
		return nil
	}
	return strings.Split(raw, "\x00")
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}
