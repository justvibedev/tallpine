package daemon

import (
	"context"
	"strings"

	claudecodeagent "tallpine.eu/tallpine/backend/internal/adapters/agent/claudecode"
)

const (
	claudeConfigDirKey        = "CLAUDE_CONFIG_DIR"
	claudeSecureStorageDirKey = "CLAUDE_SECURESTORAGE_CONFIG_DIR"
)

// claudeEnvWithProjectLogin returns the environment Claude's device-wide checks
// (readiness, model catalog) run under. Sessions launch with their project's env,
// so a Claude signed in only through a project-level CLAUDE_CONFIG_DIR works in
// every session while the daemon's own checks, which see no project, report it
// signed out. When base names no config folder and the computer's own login holds
// no sign-in, the first project (by id) whose folder does is used instead. Only
// the two folder variables are taken; local state is read, never copied or
// refreshed.
func claudeEnvWithProjectLogin(ctx context.Context, projects projectLister, base map[string]string) map[string]string {
	// A key that is present, even empty, is the account's decision (an empty value
	// clears an inherited folder so sessions use the computer's own login).
	if _, set := base[claudeConfigDirKey]; set || projects == nil || claudecodeagent.LocalLoginConfigured(ctx, base) {
		return base
	}
	records, err := projects.ListProjects(ctx)
	if err != nil {
		return base
	}
	for _, record := range records {
		dir := strings.TrimSpace(record.Config.Env[claudeConfigDirKey])
		if dir == "" {
			continue
		}
		env := map[string]string{}
		for key, value := range base {
			env[key] = value
		}
		env[claudeConfigDirKey] = dir
		if secure := strings.TrimSpace(record.Config.Env[claudeSecureStorageDirKey]); secure != "" {
			env[claudeSecureStorageDirKey] = secure
		}
		if claudecodeagent.LocalLoginConfigured(ctx, env) {
			return env
		}
	}
	return base
}
