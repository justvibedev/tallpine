package sessionmanager

import (
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// scratchTempKeys point a session's temp files at its own directory. TMPDIR is
// what Unix tools read; TMP and TEMP are the Windows and toolchain spellings.
// CLAUDE_CODE_TMPDIR is what Claude Code uses instead of its
// shared /tmp/claude-<uid> default, which it also re-exports as TMPDIR in its
// shells; without it every Claude session shares one temp dir. CLAUDE_TMPDIR is
// an output Claude exports into its shells; set only for consistency.
var scratchTempKeys = []string{"TMPDIR", "TMP", "TEMP", "CLAUDE_CODE_TMPDIR", "CLAUDE_TMPDIR"}

// envGradleUserHome is where Gradle keeps its caches, wrapper distributions and
// daemons.
const envGradleUserHome = "GRADLE_USER_HOME"

func envHasKey(env map[string]string, key string, caseInsensitive bool) bool {
	if _, ok := env[key]; ok {
		return true
	}
	if !caseInsensitive {
		return false
	}
	for existing := range env {
		if strings.EqualFold(existing, key) {
			return true
		}
	}
	return false
}

// applyScratchEnv points the session's temp files and Gradle caches under the
// data directory, so Tallpine can remove what an agent leaves behind: the temp
// directory when the session ends, the Gradle home once its project has gone
// unused. A value the project's env sets is kept, and so is a GRADLE_USER_HOME
// the daemon itself inherited: both are explicit choices.
//
// Directories are created here because a launch is about to use them. The
// teardown env does not call this, so ending a session cannot recreate the
// temp directory it just discarded.
func (m *Manager) applyScratchEnv(env map[string]string, id domain.SessionID, project domain.ProjectID, projectEnv map[string]string, caseInsensitive bool) {
	if m.dataDir == "" {
		return
	}
	projectSetsTemp := false
	for _, key := range scratchTempKeys {
		projectSetsTemp = projectSetsTemp || envHasKey(projectEnv, key, caseInsensitive)
	}
	if !projectSetsTemp {
		if dir, err := sessionscratch.EnsureTempDir(m.dataDir, id); err != nil {
			m.logger.Warn("session temp dir unavailable; agent uses the system temp dir", "session", id, "error", err)
		} else {
			setTempEnv(env, projectEnv, dir, caseInsensitive)
		}
	}
	if !m.sessionGradleHome || envHasKey(projectEnv, envGradleUserHome, caseInsensitive) ||
		strings.TrimSpace(os.Getenv(envGradleUserHome)) != "" {
		return
	}
	userGradleHome := ""
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		userGradleHome = filepath.Join(home, ".gradle")
	}
	home, err := sessionscratch.EnsureGradleHome(m.dataDir, project, userGradleHome)
	if home == "" {
		m.logger.Warn("project gradle home unavailable; agent uses the default gradle home", "session", id, "project", project, "error", err)
		return
	}
	if err != nil {
		m.logger.Warn("project gradle home: shared gradle configuration not linked", "session", id, "project", project, "error", err)
	}
	setProtectedEnv(env, envGradleUserHome, home, caseInsensitive)
}

// applyBackgroundTaskTempEnv points a background provider task's temp files at
// <workspace>/tmp, inside the task's own workspace and removed with it, rather
// than at the worker session's temp dir. A temp dir the project's env sets
// wins, as it does for the session itself.
func applyBackgroundTaskTempEnv(env, projectEnv map[string]string, workspace string, caseInsensitive bool) error {
	for _, key := range scratchTempKeys {
		if value, ok := lookupEnv(projectEnv, key, caseInsensitive); ok {
			setTempEnv(env, projectEnv, value, caseInsensitive)
			return nil
		}
	}
	dir := filepath.Join(workspace, "tmp")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	setTempEnv(env, projectEnv, dir, caseInsensitive)
	return nil
}

// setTempEnv points every temp key at dir, and zsh's TMPPREFIX inside it
// unless the project sets its own.
func setTempEnv(env, projectEnv map[string]string, dir string, caseInsensitive bool) {
	for _, key := range scratchTempKeys {
		setProtectedEnv(env, key, dir, caseInsensitive)
	}
	if !envHasKey(projectEnv, sessionscratch.TempPrefixEnv, caseInsensitive) {
		setProtectedEnv(env, sessionscratch.TempPrefixEnv, sessionscratch.TempPrefix(dir), caseInsensitive)
	}
}

func lookupEnv(env map[string]string, key string, caseInsensitive bool) (string, bool) {
	if value, ok := env[key]; ok {
		return value, true
	}
	if !caseInsensitive {
		return "", false
	}
	for existing, value := range env {
		if strings.EqualFold(existing, key) {
			return value, true
		}
	}
	return "", false
}
