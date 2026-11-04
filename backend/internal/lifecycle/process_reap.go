package lifecycle

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/sessionreap"
	"tallpine.eu/tallpine/backend/internal/sessionscratch"
)

// sessionProcessReaper stops a session's leftover processes. *sessionreap.Reaper
// implements it.
type sessionProcessReaper interface {
	Snapshot() ([]sessionreap.Proc, error)
	ReapFrom(ctx context.Context, procs []sessionreap.Proc, m sessionreap.Match) (int, error)
}

// processReapTimeout bounds one reap: the SIGTERM grace plus snapshots.
const processReapTimeout = 30 * time.Second

// sweepSettle is how long a session must have been ended before the sweep
// reaps its processes, and how old a process must be: a restore under the
// same id starts new processes that are not leftovers.
const sweepSettle = 2 * time.Minute

// reapSessionProcesses stops the processes the ended session left running:
// dev servers, headless browsers and test binaries an agent started from its
// shell, detached or not. Like the container reap it rides every terminal
// path. It runs in the background so teardown never waits for the SIGTERM
// grace, re-reads the session first, and spares processes started after this
// call, so a session restored meanwhile keeps its new launch.
func (m *Manager) reapSessionProcesses(id domain.SessionID) {
	if m.processes == nil {
		return
	}
	cutoff := time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), processReapTimeout)
		defer cancel()
		m.reapEndedSession(ctx, id, false, cutoff)
	}()
}

// SweepEndedSessionProcesses reaps processes tagged to sessions that ended
// at least sweepSettle ago: leftovers from a daemon that exited before its
// reap finished, or started by a detached parent after it. A process is
// attributed by its marker, else by an executable under a session temp dir.
// Sessions the store does not know are left alone.
func (m *Manager) SweepEndedSessionProcesses(ctx context.Context) {
	if m.processes == nil {
		return
	}
	procs, err := m.processes.Snapshot()
	if err != nil {
		slog.Default().Debug("lifecycle: process sweep: snapshot unavailable", "err", err)
		return
	}
	now := time.Now()
	seen := map[domain.SessionID]bool{}
	for _, p := range procs {
		id := domain.SessionID(p.Marker)
		if id == "" {
			id = m.tempDirSession(p.Exe)
		}
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if ctx.Err() != nil {
			return
		}
		m.reapEndedSession(ctx, id, true, now.Add(-sweepSettle))
	}
}

// tempDirSession returns the session whose temp dir holds exe, "" if none.
func (m *Manager) tempDirSession(exe string) domain.SessionID {
	if m.dataDir == "" || exe == "" {
		return ""
	}
	root := filepath.Join(m.dataDir, sessionscratch.TempRoot) + string(filepath.Separator)
	rest, ok := strings.CutPrefix(exe, root)
	if !ok {
		return ""
	}
	id, _, _ := strings.Cut(rest, string(filepath.Separator))
	if sessionscratch.TempDir(m.dataDir, domain.SessionID(id)) == "" {
		return ""
	}
	return domain.SessionID(id)
}

// reapEndedSession reaps one ended session from a snapshot taken now: the
// sweep's snapshot only names candidates, since after earlier reaps' grace
// periods its pids may have been reused by unrelated processes.
func (m *Manager) reapEndedSession(ctx context.Context, id domain.SessionID, sweep bool, cutoff time.Time) {
	rec, ok, err := m.store.GetSession(ctx, id)
	if err != nil || !ok || !rec.IsTerminated {
		return
	}
	if sweep {
		if rec.UpdatedAt.After(cutoff) {
			return // ended moments ago; the terminal-path reap owns it.
		}
		// Anything started after the session ended is not its leftover: a
		// preview server or shell the user opened on the ended session.
		cutoff = rec.UpdatedAt
	}
	procs, err := m.processes.Snapshot()
	if err != nil {
		slog.Default().Debug("lifecycle: process reap: snapshot unavailable", "session", id, "err", err)
		return
	}
	match := sessionreap.Match{SessionID: string(id), StartedBefore: cutoff}
	if dir := sessionscratch.TempDir(m.dataDir, id); dir != "" {
		match.Dirs = []string{dir}
	}
	n, err := m.processes.ReapFrom(ctx, procs, match)
	if err != nil {
		slog.Default().Warn("lifecycle: session process reap incomplete", "session", id, "err", err)
	}
	if n > 0 {
		slog.Default().Info("lifecycle: stopped processes the ended session left running", "session", id, "count", n)
	}
}
