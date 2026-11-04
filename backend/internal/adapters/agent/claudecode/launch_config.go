package claudecode

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ensureWorkspaceTrusted records workspacePath as trusted in Claude Code's
// config so the interactive trust dialog does not block a spawned session.
//
// It is additive and concurrency-safe: it reads the existing config, sets
// only projects[workspacePath].hasTrustDialogAccepted = true (preserving the
// rest of the entry and every other project), and writes back via a
// temp-file + atomic rename under Claude Code's own config lock (see
// lockClaudeConfig), re-reading the file once the lock is held so a running
// Claude cannot overwrite the entry with an older snapshot. If the path is
// already trusted, it makes no write and takes no lock. A missing config file is treated as an empty one; a
// zero-byte or corrupt config file is refused rather than overwritten.
// claudeTrustMu serializes ensureWorkspaceTrusted within the process. Concurrent
// spawns to different workspaces otherwise read the same ~/.claude.json snapshot
// and the last rename drops the other's trust entry.
var claudeTrustMu sync.Mutex

func ensureWorkspaceTrusted(configPath, workspacePath string) error {
	return ensureWorkspaceTrustedForOS(configPath, workspacePath, runtime.GOOS)
}

func ensureWorkspaceTrustedForOS(configPath, workspacePath, goos string) error {
	return ensureClaudeLaunchConfigForOS(configPath, workspacePath, false, goos)
}

// ensureClaudeLaunchConfigForOS records workspace trust and, when
// markAutoDefaultNudgeSeen is set, hasSeenAutoDefaultNudge (see
// autoDefaultNudgeGuardArgs) in one additive, atomic config write. The nudge
// flag only records that the dialog was dealt with; it never touches the
// user's permission mode.
func ensureClaudeLaunchConfigForOS(configPath, workspacePath string, markAutoDefaultNudgeSeen bool, goos string) error {
	claudeTrustMu.Lock()
	defer claudeTrustMu.Unlock()
	if goos == "windows" {
		workspacePath = strings.ReplaceAll(workspacePath, `\`, "/")
	}

	// Already prepared: no write, so no lock and no race window at all.
	if _, changed, err := applyClaudeLaunchConfig(configPath, workspacePath, markAutoDefaultNudgeSeen); err != nil || !changed {
		return err
	}

	// A CLAUDE_CONFIG_DIR Claude has not used yet may not exist; Claude creates
	// it the same way. The lock directory lives beside the config.
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("claude-code: create config directory: %w", err)
	}
	unlock, err := lockClaudeConfig(configPath)
	if err != nil {
		return err
	}
	defer unlock()

	// Re-read under the lock: a Claude process may have rewritten the file
	// since the check above.
	out, changed, err := applyClaudeLaunchConfig(configPath, workspacePath, markAutoDefaultNudgeSeen)
	if err != nil || !changed {
		return err
	}

	// Atomic write: temp file in the same directory, then rename, matching how
	// Claude Code itself replaces this file.
	tmp, err := os.CreateTemp(dir, ".claude.json.tmp-*")
	if err != nil {
		return fmt.Errorf("claude-code: create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed

	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("claude-code: write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("claude-code: close temp config: %w", err)
	}
	if err := os.Rename(tmpName, configPath); err != nil {
		return fmt.Errorf("claude-code: replace config: %w", err)
	}
	return nil
}

// applyClaudeLaunchConfig reads configPath and returns the encoded config with
// workspace trust (and, when asked, the nudge flag) recorded, and whether that
// differs from what is on disk. A missing config file is treated as an empty
// one; a zero-byte or corrupt one is refused rather than overwritten.
func applyClaudeLaunchConfig(configPath, workspacePath string, markAutoDefaultNudgeSeen bool) ([]byte, bool, error) {
	root := map[string]any{}
	data, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		if len(bytes.TrimSpace(data)) == 0 {
			// A zero-byte read means we may have caught some other writer
			// mid-truncate. Treat it like a corrupt file, not a missing
			// one: refuse to write rather than silently replacing the
			// user's real config (oauthAccount, projects history, etc.)
			// with one containing only the trust entry.
			return nil, false, fmt.Errorf("claude-code: %s is empty; refusing to overwrite", configPath)
		}
		if err := json.Unmarshal(data, &root); err != nil {
			return nil, false, fmt.Errorf("claude-code: parse %s: %w", configPath, err)
		}
	case os.IsNotExist(err):
		// Treat as empty config; we'll create it.
	default:
		return nil, false, fmt.Errorf("claude-code: read %s: %w", configPath, err)
	}

	projects, _ := root["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		root["projects"] = projects
	}

	entry, _ := projects[workspacePath].(map[string]any)
	if entry == nil {
		entry = map[string]any{}
		projects[workspacePath] = entry
	}

	changed := false
	if trusted, ok := entry["hasTrustDialogAccepted"].(bool); !ok || !trusted {
		entry["hasTrustDialogAccepted"] = true
		changed = true
	}
	if seen, _ := root["hasSeenAutoDefaultNudge"].(bool); markAutoDefaultNudgeSeen && !seen {
		root["hasSeenAutoDefaultNudge"] = true
		changed = true
	}
	if dismissAutoModeEnvSetup(root) {
		changed = true
	}
	if !changed {
		return nil, false, nil
	}

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("claude-code: encode %s: %w", configPath, err)
	}
	return out, true, nil
}

// dismissAutoModeEnvSetup records autoModeEnvSetup.dismissed = true, what
// Claude Code's "Teach auto mode about your environment?" dialog writes for
// "Don't show again" (verified in 2.1.318). Claude offers that dialog in auto
// mode once the global config counts 5 startups and 5 auto-mode denials, and it
// takes the keyboard, so a launched session's first prompt is lost (B523). The
// wizard stays available as /auto-mode-setup. Other fields of the object are
// kept; it reports whether root changed.
func dismissAutoModeEnvSetup(root map[string]any) bool {
	setup, _ := root["autoModeEnvSetup"].(map[string]any)
	if dismissed, _ := setup["dismissed"].(bool); dismissed {
		return false
	}
	if setup == nil {
		setup = map[string]any{}
	}
	setup["dismissed"] = true
	root["autoModeEnvSetup"] = setup
	return true
}
