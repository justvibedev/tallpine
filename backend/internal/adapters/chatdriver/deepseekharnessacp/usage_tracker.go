package deepseekharnessacp

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"tallpine.eu/tallpine/backend/internal/ports"
)

const dshHomeEnv = "DSH_HOME"

// usageTracker keeps one ledger per dsh session and finds each session's log.
type usageTracker struct {
	log *slog.Logger

	mu       sync.Mutex
	homes    []string // DSH_HOME values dsh was launched with, in launch order
	sessions map[string]*sessionLedger
}

func newUsageTracker(log *slog.Logger) *usageTracker {
	return &usageTracker{log: log, sessions: map[string]*sessionLedger{}}
}

// noteLaunch records the DSH_HOME dsh was started with, taken from the same
// environment the launch builds (session env plus plugin policy), since that is
// where dsh will write its logs.
func (t *usageTracker) noteLaunch(env map[string]string) {
	home := strings.TrimSpace(env[dshHomeEnv])
	if home == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, h := range t.homes {
		if h == home {
			return
		}
	}
	t.homes = append(t.homes, home)
}

// roots lists the session-log roots to search: launched homes, then the
// daemon's own DSH_HOME, then ~/.dsh. Call with t.mu held.
func (t *usageTracker) roots() []string {
	homes := append([]string(nil), t.homes...)
	if h := strings.TrimSpace(os.Getenv(dshHomeEnv)); h != "" {
		homes = append(homes, h)
	}
	if user, err := os.UserHomeDir(); err == nil && user != "" {
		homes = append(homes, filepath.Join(user, ".dsh"))
	}
	roots := make([]string, len(homes))
	for i, h := range homes {
		roots[i] = filepath.Join(h, "sessions")
	}
	return roots
}

// sessionLedger serialises one session's reads so a slow log never holds up
// another session or the tracker.
type sessionLedger struct {
	mu   sync.Mutex
	path string
	l    ledger
}

// sessionUsage is the ACP SessionUsage hook: the session's cumulative usage, or
// nil when its log is absent or unreadable.
func (t *usageTracker) sessionUsage(sessionID string) *ports.ChatUsage {
	// A dsh session id is a UUID; refuse anything that could widen the glob or
	// leave the sessions directory.
	if sessionID == "" || strings.ContainsAny(sessionID, `/\*?[`) {
		return nil
	}
	t.mu.Lock()
	entry := t.sessions[sessionID]
	if entry == nil {
		entry = &sessionLedger{}
		t.sessions[sessionID] = entry
	}
	roots := t.roots()
	t.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.path != "" {
		if _, err := os.Stat(entry.path); err != nil {
			entry.path = ""
		}
	}
	if entry.path == "" {
		for _, root := range roots {
			if matches, _ := filepath.Glob(filepath.Join(root, "*", sessionID, "session.v4.jsonl.zstd")); len(matches) > 0 {
				entry.path = matches[0]
				break
			}
		}
	}
	if entry.path == "" {
		return nil
	}
	if err := entry.l.refresh(entry.path, t.log); err != nil {
		debugf(t.log, "deepseek-harness session usage unreadable", "error", err)
	}
	return entry.l.usage()
}
