// Package nativeconfig resolves provider-owned session state directories.
package nativeconfig

import (
	"os"
	"path/filepath"
	"strings"

	"tallpine.eu/tallpine/backend/internal/agentlaunch"
)

// removed reports whether the launch removes key from the daemon environment.
func removed(env map[string]string, key string) bool {
	for _, name := range agentlaunch.UnsetNames(env) {
		if name == key {
			return true
		}
	}
	return false
}

// Resolve returns the directory selected for a provider invocation. An entry
// in env, including an explicitly empty one, takes precedence over the daemon
// environment because env describes the child process being launched.
func Resolve(env map[string]string, envKey, defaultDir string) (string, error) {
	if value, ok := env[envKey]; ok {
		if dir := strings.TrimSpace(value); dir != "" {
			return filepath.Clean(dir), nil
		}
		return homeDefault(env, defaultDir)
	}
	// A name the launch removes from the daemon environment resolves as the
	// launched provider sees it: unset, so the default folder.
	if removed(env, envKey) {
		return homeDefault(env, defaultDir)
	}
	if dir := strings.TrimSpace(os.Getenv(envKey)); dir != "" {
		return filepath.Clean(dir), nil
	}
	return homeDefault(env, defaultDir)
}

func homeDefault(env map[string]string, defaultDir string) (string, error) {
	if home := strings.TrimSpace(env["HOME"]); home != "" {
		return filepath.Join(home, defaultDir), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, defaultDir), nil
}

// CodexHome returns the CODEX_HOME a Codex invocation with env reads, falling
// back to ~/.codex. It is the single resolver for rollout lookup, auth
// identity and session config dir.
func CodexHome(env map[string]string) (string, error) {
	return Resolve(env, "CODEX_HOME", ".codex")
}

// ClaudeConfigDir returns the directory a Claude invocation with env reads and
// the path of its global config. With CLAUDE_CONFIG_DIR set (an explicitly
// empty env entry counts as unset) the global config is <dir>/.claude.json;
// otherwise it is ~/.claude.json. A non-empty legacy <dir>/.config.json wins,
// as in Claude itself. An unresolvable home is an error, never a relative path.
func ClaudeConfigDir(env map[string]string) (dir, globalConfig string, err error) {
	dir, err = Resolve(env, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return "", "", err
	}
	globalDir := filepath.Dir(dir)
	if explicit(env, "CLAUDE_CONFIG_DIR") {
		globalDir = dir
	}
	globalConfig = filepath.Join(globalDir, ".claude.json")
	legacy := filepath.Join(dir, ".config.json")
	if info, statErr := os.Stat(legacy); statErr == nil && info.Mode().IsRegular() && info.Size() > 0 { // #nosec G703 -- existence check of a path under the configured config dir
		globalConfig = legacy
	}
	return dir, globalConfig, nil
}

func explicit(env map[string]string, key string) bool {
	if value, ok := env[key]; ok {
		return strings.TrimSpace(value) != ""
	}
	if removed(env, key) {
		return false
	}
	return strings.TrimSpace(os.Getenv(key)) != ""
}
