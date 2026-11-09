package tmux

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func validateEnvKeys(env map[string]string) error {
	for key := range env {
		if !validEnvKey(key) {
			return fmt.Errorf("tmux runtime: invalid env key %q", key)
		}
	}
	return nil
}

func validEnvKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// buildLaunchCommand builds the shell command string passed to `sh -c`. It
// exports env vars and runs argv. Short-lived command terminals exit with the
// command; ordinary interactive runtimes keep the tmux session alive. Supervised
// launches park on a non-interpreting stdin sink after exit so bytes racing a
// process exit can never become shell commands; legacy/unsupervised launches
// retain the interactive-shell fallback used by manual recovery.
//
// PATH from cfg.Env is exported last, after all other keys, so an explicit
// override takes effect.
func buildLaunchCommand(cfg ports.RuntimeConfig) string {
	return buildLaunchCommandWithEnvFile(cfg, "")
}

// buildLaunchCommandWithEnvFile is buildLaunchCommand with the session env
// moved out of the command line: when envFile is non-empty the command sources
// that file (see launchEnvFileContent) and deletes it, so no env value appears
// in any process argv.
func buildLaunchCommandWithEnvFile(cfg ports.RuntimeConfig, envFile string) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}

	var b strings.Builder
	b.WriteString("cd ")
	b.WriteString(shellQuote(cfg.WorkspacePath))
	if envFile != "" {
		// A missing workspace must not strand the env file.
		b.WriteString(" || { rm -f " + shellQuote(envFile) + "; exit 1; }; ")
	} else {
		b.WriteString(" || exit; ")
	}
	if agentlaunch.Scrubbed(cfg.Env) {
		return buildScrubbedLaunchCommand(&b, cfg, path, envFile)
	}
	// Daemon-inherited variables the agent policy withholds (the tmux server's
	// environment is the daemon's): removed before the session's own exports, so
	// a value the project's environment sets for the same name still applies.
	if names := agentlaunch.UnsetNames(cfg.Env); len(names) > 0 {
		b.WriteString("unset ")
		b.WriteString(strings.Join(names, " "))
		b.WriteString("; ")
	}
	if _, configured := cfg.Env["NO_COLOR"]; !configured {
		// The daemon may be launched from another agent or CI environment that
		// sets NO_COLOR for its own captured output. Do not leak that ambient
		// preference into an interactive terminal session. A project can still
		// opt out of color explicitly through its configured environment.
		b.WriteString("unset NO_COLOR; ")
	}
	if envFile == "" {
		b.WriteString(exportBlock(cfg, path))
	} else {
		b.WriteString(". " + shellQuote(envFile) + "; rm -f " + shellQuote(envFile) + "; ")
	}
	// Quote each argv word so spaces inside a word are preserved.
	parts := make([]string, len(cfg.Argv))
	for i, a := range cfg.Argv {
		parts[i] = shellQuote(a)
	}
	b.WriteString(strings.Join(parts, " "))
	if cfg.ExitOnCommandCompletion {
		// Let the tmux session disappear as soon as its one backend-owned command
		// completes. The terminal mux then emits `exited`, which drives exact
		// post-command work such as Codex account verification.
		b.WriteString(`; exit $?`)
	} else if cfg.Env["TALLPINE_SUPERVISED_PROCESS"] == "1" {
		// cat consumes and discards any input that arrived while the supervised
		// child was exiting. Runtime Restart/Destroy replaces or kills the pane.
		b.WriteString(`; exec cat >/dev/null`)
	} else {
		// Keep the tmux session alive after an unsupervised agent exits so the
		// operator can inspect it and use the historical manual-recovery shell.
		b.WriteString(`; exec "${SHELL:-/bin/sh}" -i`)
	}
	return b.String()
}

// exportBlock is the `export K=v; ...` text for a non-scrubbed launch. PATH is
// exported last so an explicit override takes effect.
func exportBlock(cfg ports.RuntimeConfig, path string) string {
	var b strings.Builder
	for _, key := range sortedKeys(cfg.Env) {
		if key == "PATH" || key == "COLORTERM" || key == agentlaunch.EnvUnset || key == agentlaunch.EnvScrub {
			continue
		}
		b.WriteString("export ")
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(shellQuote(cfg.Env[key]))
		b.WriteString("; ")
	}
	// The Tallpine web terminal and tmux attach client both support 24-bit SGR color.
	// Export this after caller env so agent color detection cannot accidentally
	// downgrade rich syntax/diff colors to ANSI-256.
	b.WriteString("export COLORTERM='truecolor'; ")
	if path != "" {
		b.WriteString("export PATH=")
		b.WriteString(shellQuote(path))
		b.WriteString("; ")
	}
	return b.String()
}

// launchEnvFileContent is the shell text sourced by a launch whose env travels
// in a file instead of argv.
func launchEnvFileContent(cfg ports.RuntimeConfig) string {
	path := cfg.Env["PATH"]
	if path == "" {
		path = getenv("PATH")
	}
	if agentlaunch.Scrubbed(cfg.Env) {
		return scrubbedAssignments(cfg, path)
	}
	return exportBlock(cfg, path)
}

// writeLaunchEnvFile stores the launch env in a 0600 file inside a 0700
// directory under the temp dir, for the launch command to source and delete.
func writeLaunchEnvFile(cfg ports.RuntimeConfig) (string, error) {
	dir := agentlaunch.EnvFileDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // directory, 0700 is owner-only.
		return "", err
	}
	f, err := os.CreateTemp(dir, "env-*")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(launchEnvFileContent(cfg))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	return f.Name(), nil
}
