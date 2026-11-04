package shellterm

import (
	"context"
	"fmt"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/httpd/apierr"
)

// resolveShellTerminalWorkingDir picks where the shell starts. A session id
// takes precedence: the shell lands in that session's live workspace (its
// worktree), so it stays colocated with the agent even though that worktree
// differs from the project's registered root. A session with no workspace yet
// (or no session id at all) falls back to the project root, then the daemon's
// data dir.
// It also returns the project the shell ended up attributed to, which is not
// always the requested one: a session-scoped open may carry no project id, and
// the session's own project is what the persisted row should record.
func (s *Service) resolveShellTerminalWorkingDir(ctx context.Context, projectID domain.ProjectID, sessionID domain.SessionID) (workingDir string, resolvedProjectID domain.ProjectID, err error) {
	if sessionID != "" {
		if s.sessions == nil {
			return "", "", apierr.Internal("SHELL_TERMINAL_NO_SESSION_LOOKUP", "Session lookup is unavailable")
		}
		workspacePath, sessionProjectID, err := s.sessions.SessionWorkspace(ctx, sessionID)
		if err != nil {
			return "", "", fmt.Errorf("open shell terminal: resolve session %s: %w", sessionID, err)
		}
		// The session is authoritative even when it is standalone. Keeping a
		// caller-supplied UI sentinel here would violate shell_terminals'
		// project foreign key after the PTY has already been created.
		projectID = sessionProjectID
		if workspacePath != "" {
			return workspacePath, projectID, nil
		}
	}
	dir, err := s.resolveProjectRootOrDataDir(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	return dir, projectID, nil
}

// resolveProjectRootOrDataDir picks the project root when a project is named,
// else the daemon's data dir.
func (s *Service) resolveProjectRootOrDataDir(ctx context.Context, projectID domain.ProjectID) (string, error) {
	if projectID == "" {
		if s.dataDir == "" {
			return "", apierr.Internal("SHELL_TERMINAL_NO_WORKING_DIR",
				"No project selected and the daemon has no data dir to fall back to")
		}
		return s.dataDir, nil
	}
	if s.projects == nil {
		return "", apierr.Internal("SHELL_TERMINAL_NO_PROJECT_LOOKUP",
			"Project lookup is unavailable")
	}
	root, err := s.projects.ProjectRoot(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("open shell terminal: resolve project %s: %w", projectID, err)
	}
	if root == "" {
		return "", apierr.NotFound("SHELL_TERMINAL_PROJECT_NOT_FOUND",
			"No such project: "+string(projectID))
	}
	return root, nil
}
