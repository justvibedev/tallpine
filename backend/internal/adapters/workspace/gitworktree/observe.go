package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const (
	maxObservedWorkspaceChanges = 500
	maxObservedWorkspaceCommits = 20
)

// ObserveWorkspace returns a bounded, read-only Git snapshot for handoff and
// recovery. It deliberately lives in the workspace adapter so the session
// manager does not grow an out-of-band dependency on the git executable.
func (w *Workspace) ObserveWorkspace(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceObservation, error) {
	path := strings.TrimSpace(info.Path)
	if path == "" {
		return ports.WorkspaceObservation{}, errors.New("gitworktree: observe workspace path is required")
	}
	validated, err := w.validateManagedPath(path)
	if err != nil {
		return ports.WorkspaceObservation{}, err
	}
	path = validated

	head, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe HEAD: %w", err)
	}
	branch := strings.TrimSpace(info.Branch)
	if out, branchErr := w.run(ctx, w.binary, "-C", path, "branch", "--show-current"); branchErr == nil {
		if current := strings.TrimSpace(string(out)); current != "" {
			branch = current
		}
	}
	statusOut, err := w.run(ctx, w.binary, "-C", path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe status: %w", err)
	}
	changes, staged, untracked := parseObservedWorkspaceChanges(string(statusOut), maxObservedWorkspaceChanges)

	// Unit and record separators avoid ambiguity from spaces in subjects. Git
	// subjects cannot contain a newline, and the bounded output is local-only.
	logFormat := "%H%x1f%s%x1f%aI%x1e"
	logOut, err := w.run(ctx, w.binary, "-C", path, "log", "-n", fmt.Sprintf("%d", maxObservedWorkspaceCommits), "--pretty=format:"+logFormat)
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe log: %w", err)
	}
	return ports.WorkspaceObservation{
		Path:      path,
		Branch:    branch,
		HeadSHA:   strings.TrimSpace(string(head)),
		Dirty:     len(changes) > 0,
		Staged:    staged,
		Untracked: untracked,
		Changes:   changes,
		Commits:   parseObservedWorkspaceCommits(string(logOut)),
		Landed:    w.observeLanded(ctx, path, strings.TrimSpace(string(head)), info),
	}, nil
}

// observeLanded reports whether HEAD is already contained in the project base
// ref. It reads local refs only and treats every failure, including a missing
// base, as "not landed". A HEAD still at the session's creation commit is not
// landed: it is trivially contained in the base it was cut from.
func (w *Workspace) observeLanded(ctx context.Context, path, head string, info ports.WorkspaceInfo) bool {
	base := strings.TrimSpace(info.BaseRef)
	// "HEAD" is the spawn fallback when no default branch resolved, not a base.
	if base == "" || base == "HEAD" || base == "@" || head == "" || head == strings.TrimSpace(info.BaseSHA) {
		return false
	}
	out, err := w.run(ctx, w.binary, "-C", path, "rev-list", "-n", "1", "HEAD", "--not", base, "--")
	return err == nil && strings.TrimSpace(string(out)) == ""
}

func parseObservedWorkspaceChanges(output string, limit int) ([]ports.WorkspaceChange, bool, bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	changes := make([]ports.WorkspaceChange, 0, min(len(lines), limit))
	var staged, untracked bool
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if status == "??" {
			untracked = true
		} else if status[0] != ' ' && status[0] != '?' {
			staged = true
		}
		if len(changes) < limit {
			changes = append(changes, ports.WorkspaceChange{Path: path, Status: status})
		}
	}
	return changes, staged, untracked
}

func parseObservedWorkspaceCommits(output string) []ports.WorkspaceCommit {
	records := strings.Split(output, "\x1e")
	commits := make([]ports.WorkspaceCommit, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		commits = append(commits, ports.WorkspaceCommit{
			SHA:        strings.TrimSpace(fields[0]),
			Subject:    strings.TrimSpace(fields[1]),
			AuthoredAt: strings.TrimSpace(fields[2]),
		})
	}
	return commits
}
