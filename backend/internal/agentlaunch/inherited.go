package agentlaunch

import (
	"os"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/brand"
)

// inheritedHostIdentityKeys are per-process identity variables that an agent
// host exports to its own children. They describe the process that launched
// the daemon, never a session the daemon launches. Every daemon child inherits
// the daemon's environment, so when the desktop app or `tallpine start` is run from
// inside an agent (`open` on macOS forwards the caller's environment), each
// worker would otherwise run as a child of that foreign session:
//
//   - CLAUDE_CODE_ENTRYPOINT=sdk-ts makes the worker's hooks look like a
//     headless side run, so lifecycle drops every one of them: no native
//     session id, no transcript, zero turns, activity stuck idle.
//   - CLAUDECODE / CLAUDE_CODE_SESSION_ID / CLAUDE_CODE_CHILD_SESSION and the
//     messaging socket attach the worker to the launching Claude session.
//   - VO_* worker identity names a session of whichever daemon owns the shell.
var inheritedHostIdentityKeys = map[string]struct{}{
	// Claude Code host session.
	"AI_AGENT":                              {},
	"CLAUDECODE":                            {},
	"CLAUDE_AGENT_SDK_VERSION":              {},
	"CLAUDE_CODE_CHILD_SESSION":             {},
	"CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS": {},
	"CLAUDE_CODE_ENTRYPOINT":                {},
	"CLAUDE_CODE_EXECPATH":                  {},
	"CLAUDE_CODE_MESSAGING_SOCKET":          {},
	"CLAUDE_CODE_MESSAGING_TOKEN":           {},
	"CLAUDE_CODE_REMOTE_SESSION_ID":         {},
	"CLAUDE_CODE_SESSION_ATTENDED":          {},
	"CLAUDE_CODE_SESSION_ID":                {},
	"CLAUDE_CODE_SESSION_LOG":               {},
	"CLAUDE_CODE_SSE_PORT":                  {},
	"CLAUDE_ENV_FILE":                       {},
	"CLAUDE_PID":                            {},
	"CLAUDE_PROJECT_DIR":                    {},
	// Codex host session.
	"CODEX_SANDBOX":                  {},
	"CODEX_SANDBOX_NETWORK_DISABLED": {},
	"CODEX_THREAD_ID":                {},
	// A session of this product's own daemon (e.g. `tallpine start` from a
	// worker). The daemon sets each of these explicitly on the launches that need them.
	brand.EnvPrefix + "ALLOW_SHARED_GIT_CONFIG": {},
	brand.EnvPrefix + "BROWSER_CAPABILITY":      {},
	brand.EnvPrefix + "GIT_GUARD_ROOT":          {},
	brand.EnvPrefix + "ISSUE_ID":                {},
	brand.EnvPrefix + "PERMISSION_MODE":         {},
	brand.EnvPrefix + "PROJECT_ID":              {},
	brand.EnvPrefix + "RUNTIME_LAUNCH_ID":       {},
	brand.EnvPrefix + "SESSION":                 {},
	brand.EnvPrefix + "SESSION_ID":              {},
	brand.EnvPrefix + "SESSION_TOKEN":           {},
	brand.EnvPrefix + "SESSION_TOKEN_RUN_FILE":  {},
	brand.EnvPrefix + "SESSION_AUTH":            {},
	brand.EnvPrefix + "SESSION_AUTH_RUN_FILE":   {},
	// Marks an in-app user shell terminal; a daemon started from one must not
	// pass it to session processes, or the session reaper spares them all.
	brand.EnvPrefix + "SHELL_TERMINAL":       {},
	brand.EnvPrefix + "SIDE_CONVERSATION_ID": {},
	brand.EnvPrefix + "SUPERVISED_PROCESS":   {},
}

// officialEnvPrefix is the official Agent Orchestrator's namespace. This
// fork never reads it, so an inherited AO_* value can only be the official
// install's coordinates (AO_RUN_FILE, AO_DATA_DIR, AO_SESSION_ID, ...). Left in
// place, an `ao` command or official hook running inside a tallpine worker would act
// on the official daemon as the official session that launched tallpine.
const officialEnvPrefix = "AO_"

// InheritedHostIdentityKeys returns the sorted, de-duplicated keys in environ
// ("KEY=value" entries) that ScrubInheritedHostEnv removes.
func InheritedHostIdentityKeys(environ []string) []string {
	seen := make(map[string]struct{})
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if key == "" || !isInheritedHostIdentityKey(key) {
			continue
		}
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func isInheritedHostIdentityKey(key string) bool {
	upper := strings.ToUpper(key)
	if strings.HasPrefix(upper, officialEnvPrefix) {
		return true
	}
	_, ok := inheritedHostIdentityKeys[upper]
	return ok
}

// ScrubInheritedHostEnv removes inherited agent-host identity from the current
// process environment and returns the removed keys (never values: several are
// credentials). The daemon calls it once at startup, before it spawns anything,
// so every runtime, chat host, and helper starts from its own identity only.
func ScrubInheritedHostEnv() []string {
	keys := InheritedHostIdentityKeys(os.Environ())
	for _, key := range keys {
		_ = os.Unsetenv(key)
	}
	return keys
}
