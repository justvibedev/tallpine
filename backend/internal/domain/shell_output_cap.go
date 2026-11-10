package domain

import (
	"os"
	"strconv"
	"strings"
)

const (
	// EnvShellOutputLimit overrides the shell-output cap, in characters, for
	// sessions Tallpine launches. "0" or "off" keeps each harness's own default.
	// A project env entry wins over the daemon's environment.
	EnvShellOutputLimit = "TALLPINE_SHELL_OUTPUT_LIMIT"
	// DefaultShellOutputLimit is the cap in characters (about 3k tokens): well
	// under the harness defaults (Claude 30000 characters, Codex 10000 tokens),
	// still roomy enough for a typical test or build summary.
	DefaultShellOutputLimit = 12000
)

// ShellOutputLimit returns the cap on the shell output a Claude Code session's
// agent reads inline, in characters; 0 means leave the harness default. Claude
// saves the whole output to a file and shows a preview and its path, and does
// not cut failed commands. Claude clamps the value to 4000-128000. Codex has no
// full-output file, so it is left unchanged.
func ShellOutputLimit(projectEnv map[string]string) int {
	raw := projectEnv[EnvShellOutputLimit]
	if strings.TrimSpace(raw) == "" {
		raw = os.Getenv(EnvShellOutputLimit)
	}
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "":
		return DefaultShellOutputLimit
	case "0", "off":
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return DefaultShellOutputLimit
	}
	return n
}
