package sessionmanager

import (
	"context"
	"os"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/domain"
)

// applyGitGuard puts the git shim first on the launch PATH for every harness,
// with the project's checkout as what it protects: linked worktrees share that
// checkout's remotes and .git/config (B600). Project env cannot set its
// variables (agentlaunch.MergeEnv drops the reserved prefix). A session with no
// project or no shim launches without the guard. Only Claude Code is sure to
// keep this PATH: Codex and Kiro shells re-run the user's rc files, which can put
// the real git back in front (see shimCanBeBypassed). The pre-tool hook refuses
// those writes without relying on PATH (cli.enforceGitGuard), reading the guard
// root set here; Kiro chat loads no hooks, so it has only this shim.
func (m *Manager) applyGitGuard(project domain.ProjectID, env map[string]string, caseInsensitive bool) {
	if env == nil {
		return
	}
	if project == "" || m.dataDir == "" || m.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), githubAccountEnvTimeout)
	defer cancel()
	row, err := m.loadProject(ctx, project)
	if err != nil || strings.TrimSpace(row.Path) == "" {
		return
	}
	exe, err := m.githubHelperExecutable()
	if err != nil {
		m.logger.Warn("git shim unavailable; remote and shared config writes are not refused in this session", "project", project, "error", err)
		return
	}
	dir, err := agentlaunch.EnsureGitShim(exe, m.dataDir)
	if err != nil {
		m.logger.Warn("git shim unavailable; remote and shared config writes are not refused in this session", "project", project, "error", err)
		return
	}
	if dir == "" {
		return
	}
	setProtectedEnv(env, agentlaunch.EnvGitGuardRoot, row.Path, caseInsensitive)
	path, _ := lookupEnv(env, "PATH", caseInsensitive)
	if path != "" {
		dir += string(os.PathListSeparator) + path
	}
	setProtectedEnv(env, "PATH", dir, caseInsensitive)
}
