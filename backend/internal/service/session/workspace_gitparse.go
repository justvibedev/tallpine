package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

func workspaceStatuses(ctx context.Context, root string) (map[string]WorkspaceFileStatus, map[string]string, error) {
	out, err := gitWorkspaceOutput(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, nil, err
	}
	parts := splitNUL(out)
	statuses := map[string]WorkspaceFileStatus{}
	previous := map[string]string{}
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if len(entry) < 4 {
			continue
		}
		xy := entry[:2]
		rel := filepath.ToSlash(entry[3:])
		statuses[rel] = classifyWorkspaceStatus(xy)
		if strings.ContainsAny(xy, "RC") && i+1 < len(parts) {
			previous[rel] = filepath.ToSlash(parts[i+1])
			i++
		}
	}
	return statuses, previous, nil
}

func classifyWorkspaceStatus(xy string) WorkspaceFileStatus {
	switch {
	case xy == "??", strings.Contains(xy, "A"):
		return WorkspaceFileAdded
	case strings.ContainsAny(xy, "RC"):
		return WorkspaceFileRenamed
	case strings.Contains(xy, "D"):
		return WorkspaceFileDeleted
	case strings.ContainsAny(xy, "M"):
		return WorkspaceFileModified
	default:
		return WorkspaceFileModified
	}
}

// workspaceDiffStats obtains status, rename sources and line counts in one
// Git traversal. Separate name-status and numstat calls each rescan the worktree.
func workspaceDiffStats(ctx context.Context, root string, revArgs ...string) (map[string]WorkspaceFileStatus, map[string]string, map[string][2]int, error) {
	args := append([]string{"diff", "--raw", "--numstat", "--find-renames", "-z"}, revArgs...)
	args = append(args, "--")
	out, err := gitWorkspaceOutput(ctx, root, args...)
	if err != nil {
		return nil, nil, nil, err
	}
	parts := splitNUL(out)
	statuses := map[string]WorkspaceFileStatus{}
	previous := map[string]string{}
	index := 0
	for index < len(parts) && strings.HasPrefix(parts[index], ":") {
		fields := strings.Fields(parts[index])
		if len(fields) != 5 || index+1 >= len(parts) {
			return nil, nil, nil, fmt.Errorf("invalid Git raw diff record")
		}
		code := fields[4]
		oldPath := filepath.ToSlash(parts[index+1])
		newPath := oldPath
		index += 2
		status := classifyNameStatus(code)
		if status == WorkspaceFileRenamed {
			if index >= len(parts) {
				return nil, nil, nil, fmt.Errorf("invalid Git rename record")
			}
			newPath = filepath.ToSlash(parts[index])
			index++
			previous[newPath] = oldPath
		}
		statuses[newPath] = status
	}
	return statuses, previous, parseNumstatOutput(strings.Join(parts[index:], "\x00")), nil
}

// workspaceDiffNameStatus runs `git diff --name-status` with the given
// revision arguments (e.g. a single base for base..worktree, "--cached" for
// index..HEAD, or two revisions for a committed range) and parses the result.
func workspaceDiffNameStatus(ctx context.Context, root string, revArgs ...string) (map[string]WorkspaceFileStatus, map[string]string, error) {
	return workspaceDiffNameStatusPaths(ctx, root, revArgs, nil)
}

func workspaceDiffNameStatusPaths(ctx context.Context, root string, revArgs, paths []string) (map[string]WorkspaceFileStatus, map[string]string, error) {
	args := append([]string{"diff", "--name-status", "--find-renames", "-z"}, revArgs...)
	args = append(args, "--")
	args = append(args, paths...)
	out, err := gitWorkspaceOutput(ctx, root, args...)
	if err != nil {
		return nil, nil, err
	}
	statuses, previous := parseNameStatusOutput(out)
	return statuses, previous, nil
}

func parseNameStatusOutput(out string) (map[string]WorkspaceFileStatus, map[string]string) {
	parts := splitNUL(out)
	statuses := map[string]WorkspaceFileStatus{}
	previous := map[string]string{}
	for i := 0; i < len(parts); {
		statusCode, paths, next := parseGitStatusRecord(parts, i)
		i = next
		if statusCode == "" || len(paths) == 0 {
			continue
		}
		status := classifyNameStatus(statusCode)
		rel := filepath.ToSlash(paths[len(paths)-1])
		statuses[rel] = status
		if status == WorkspaceFileRenamed && len(paths) > 1 {
			previous[rel] = filepath.ToSlash(paths[0])
		}
	}
	return statuses, previous
}

func parseGitStatusRecord(parts []string, i int) (string, []string, int) {
	if i >= len(parts) {
		return "", nil, i + 1
	}
	token := parts[i]
	if fields := strings.Split(token, "\t"); len(fields) >= 2 {
		status := fields[0]
		if status == "" {
			return "", nil, i + 1
		}
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			if len(fields) >= 3 {
				return status, []string{fields[1], fields[2]}, i + 1
			}
			return status, fields[1:], i + 1
		}
		return status, []string{fields[1]}, i + 1
	}
	status := token
	if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
		if i+2 >= len(parts) {
			return status, nil, len(parts)
		}
		return status, []string{parts[i+1], parts[i+2]}, i + 3
	}
	if i+1 >= len(parts) {
		return status, nil, len(parts)
	}
	return status, []string{parts[i+1]}, i + 2
}

func classifyNameStatus(status string) WorkspaceFileStatus {
	switch status[0] {
	case 'A':
		return WorkspaceFileAdded
	case 'D':
		return WorkspaceFileDeleted
	case 'R', 'C':
		return WorkspaceFileRenamed
	case 'M', 'T':
		return WorkspaceFileModified
	default:
		return WorkspaceFileModified
	}
}

// workspaceDiffNumstat runs `git diff --numstat` with the given revision
// arguments; see workspaceDiffNameStatus for the argument forms.
func workspaceDiffNumstat(ctx context.Context, root string, revArgs ...string) (map[string][2]int, error) {
	return workspaceDiffNumstatPaths(ctx, root, revArgs, nil)
}

func workspaceDiffNumstatPaths(ctx context.Context, root string, revArgs, paths []string) (map[string][2]int, error) {
	args := append([]string{"diff", "--numstat", "--find-renames", "-z"}, revArgs...)
	args = append(args, "--")
	args = append(args, paths...)
	out, err := gitWorkspaceOutput(ctx, root, args...)
	if err != nil {
		return nil, err
	}
	return parseNumstatOutput(out), nil
}

func parseNumstatOutput(out string) map[string][2]int {
	counts := map[string][2]int{}
	parts := splitNUL(out)
	for i := 0; i < len(parts); {
		fields := strings.SplitN(parts[i], "\t", 3)
		i++
		if len(fields) < 3 {
			continue
		}
		additions, addOK := parseNumstatField(fields[0])
		deletions, delOK := parseNumstatField(fields[1])
		if !addOK || !delOK {
			if fields[2] == "" && i+2 <= len(parts) {
				i += 2
			}
			continue
		}
		rel := fields[2]
		if rel == "" && i+2 <= len(parts) {
			rel = parts[i+1]
			i += 2
		}
		if rel == "" {
			continue
		}
		counts[filepath.ToSlash(rel)] = [2]int{additions, deletions}
	}
	return counts
}

func parseNumstatField(raw string) (int, bool) {
	if raw == "-" {
		return 0, false
	}
	n, err := strconv.Atoi(raw)
	return n, err == nil
}
