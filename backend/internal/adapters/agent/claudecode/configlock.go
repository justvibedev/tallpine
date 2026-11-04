package claudecode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// Claude Code serializes every write of its global config through a
// proper-lockfile lock: the directory <config>.lock, created with mkdir and
// treated as stale once its mtime is older than ten seconds. Each Claude
// process re-reads the config under that lock and writes back its own merge.
// An unlocked read-modify-rename can land between one of those re-reads and
// its write, and the trust entry Tallpine just recorded is silently lost: the worker
// then sits on the "do you trust this folder?" dialog with no hooks, no
// transcript and no turns. Busy machines (another orchestrator's workers, Tallpine's
// own concurrent title and branch-naming `claude -p` runs) hit that window.
var (
	claudeConfigLockStale   = 10 * time.Second
	claudeConfigLockTimeout = 15 * time.Second
	claudeConfigLockPoll    = 20 * time.Millisecond
)

// lockClaudeConfig takes Claude Code's own config lock for configPath and
// returns its release. A lock left behind by a holder that died is reclaimed
// once stale, as proper-lockfile does; a live holder is waited for up to
// claudeConfigLockTimeout.
func lockClaudeConfig(configPath string) (func(), error) {
	lockPath := configPath + ".lock"
	deadline := time.Now().Add(claudeConfigLockTimeout)
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("claude-code: lock %s: %w", configPath, err)
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > claudeConfigLockStale {
			_ = os.Remove(lockPath)
			continue
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("claude-code: %s is held by another process; gave up after %s", lockPath, claudeConfigLockTimeout)
		}
		time.Sleep(claudeConfigLockPoll)
	}
}
