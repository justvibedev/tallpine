package agentlaunch

import (
	"regexp"
	"runtime"
	"strings"
)

// EnvUnset is a reserved key of an agent's launch environment map. Its value is
// a comma-separated list of variable names the launch must remove from the
// environment it inherits from the daemon (never a value the map itself sets).
// The agent policy's GitHub helper mode uses it to keep the daemon's own
// GH_TOKEN and friends out of agents.
//
// A map is the only thing every launch path shares (tmux, the pty host, Chat
// provider processes), so each of them honours this key where it merges the
// daemon environment, and none of them passes the key itself on.
const EnvUnset = "TALLPINE_UNSET_ENV"

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// UnsetNames returns the valid variable names listed in env[EnvUnset].
func UnsetNames(env map[string]string) []string {
	raw, ok := env[EnvUnset]
	if !ok {
		return nil
	}
	var names []string
	for _, name := range strings.Split(raw, ",") {
		if name = strings.TrimSpace(name); envName.MatchString(name) {
			names = append(names, name)
		}
	}
	return names
}

// SetUnset records names in env[EnvUnset] (nothing for an empty list).
func SetUnset(env map[string]string, names []string) {
	if len(names) == 0 {
		return
	}
	env[EnvUnset] = strings.Join(names, ",")
}

// FilterUnset returns environ ("KEY=value" entries) without the named variables;
// names are compared case-insensitively on Windows.
func FilterUnset(environ, names []string) []string {
	if len(names) == 0 {
		return environ
	}
	fold := runtime.GOOS == "windows"
	drop := make(map[string]bool, len(names))
	for _, n := range names {
		if fold {
			n = strings.ToUpper(n)
		}
		drop[n] = true
	}
	out := make([]string, 0, len(environ))
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		if fold {
			key = strings.ToUpper(key)
		}
		if !drop[key] {
			out = append(out, entry)
		}
	}
	return out
}

// EnvGHShimDir names the directory holding a session's `gh` shim. `tallpine gh-exec`
// skips it when it looks for the real gh, so the shim does not run itself.
const EnvGHShimDir = "TALLPINE_GH_SHIM_DIR"

// EnvScrub is a reserved key of an agent's launch environment map: set to "1", it
// makes that map the whole environment: the launch must not let the agent inherit the daemon's (or
// an already-running terminal server's) variables. The agent policy's env
// allowlist uses it; the map then already holds the allowed daemon variables.
// Like EnvUnset it instructs the launch and is never passed on.
const EnvScrub = "TALLPINE_SCRUB_ENV"

// Scrubbed reports whether env asks for a scrubbed launch.
func Scrubbed(env map[string]string) bool { return env[EnvScrub] == "1" }

// WithoutControlKeys returns env without EnvUnset and EnvScrub, which instruct
// the launch and are not passed on to the agent.
func WithoutControlKeys(env map[string]string) map[string]string {
	_, unset := env[EnvUnset]
	_, scrub := env[EnvScrub]
	if !unset && !scrub {
		return env
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if k != EnvUnset && k != EnvScrub {
			out[k] = v
		}
	}
	return out
}
