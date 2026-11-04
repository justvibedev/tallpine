package tmux

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// scrubbedEnvPrefix is `env -i` plus the session's whole environment. A pane
// otherwise starts from the tmux server's environment, which an already-running
// server took from whatever launched it, so only `env -i` gives an exact result.
// TERM comes from the pane itself (tmux sets it per pane), not from the daemon.
func scrubbedEnvPrefix(cfg ports.RuntimeConfig, path string, fromFile bool) string {
	val := func(key, v string) string {
		if fromFile {
			return `"$` + key + `"`
		}
		return shellQuote(v)
	}
	var b strings.Builder
	b.WriteString(`env -i TERM="$TERM" COLORTERM='truecolor'`)
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" || key == "COLORTERM" || key == "TERM" || key == agentlaunch.EnvUnset || key == agentlaunch.EnvScrub {
			continue
		}
		b.WriteString(" ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(val(key, cfg.Env[key]))
	}
	if path != "" {
		b.WriteString(" PATH=")
		b.WriteString(val("PATH", path))
	}
	return b.String()
}

// buildScrubbedLaunchCommand finishes buildLaunchCommand for a scrubbed launch:
// the agent, and the recovery shell that follows an unsupervised agent, both
// start from the scrubbed environment instead of the pane's.
func buildScrubbedLaunchCommand(b *strings.Builder, cfg ports.RuntimeConfig, path, envFile string) string {
	if envFile != "" {
		b.WriteString(". " + shellQuote(envFile) + "; rm -f " + shellQuote(envFile) + "; ")
	}
	prefix := scrubbedEnvPrefix(cfg, path, envFile != "")
	parts := make([]string, len(cfg.Argv))
	for i, a := range cfg.Argv {
		parts[i] = shellQuote(a)
	}
	b.WriteString(prefix)
	b.WriteString(" ")
	b.WriteString(strings.Join(parts, " "))
	switch {
	case cfg.ExitOnCommandCompletion:
		b.WriteString(`; exit $?`)
	case cfg.Env["TALLPINE_SUPERVISED_PROCESS"] == "1":
		b.WriteString(`; exec cat >/dev/null`)
	default:
		b.WriteString("; exec " + prefix + ` "${SHELL:-/bin/sh}" -i`)
	}
	return b.String()
}

// scrubbedAssignments defines the shell variables a file-sourced scrubbed
// launch forwards to `env -i` (see scrubbedEnvPrefix).
func scrubbedAssignments(cfg ports.RuntimeConfig, path string) string {
	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" || key == "COLORTERM" || key == "TERM" || key == agentlaunch.EnvUnset || key == agentlaunch.EnvScrub {
			continue
		}
		b.WriteString(key + "=" + shellQuote(cfg.Env[key]) + "\n")
	}
	if path != "" {
		b.WriteString("PATH=" + shellQuote(path) + "\n")
	}
	return b.String()
}
