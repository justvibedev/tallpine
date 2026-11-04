// Package sessionreap finds and stops the processes an ended session left
// running. Agents start dev servers, headless browsers and test binaries from
// their shell, often detached (setsid, nohup, &), so those escape the session's
// process group and outlive `tallpine session kill`. Every process the session
// launches inherits TALLPINE_SESSION_ID, so the reaper selects by that marker,
// by an executable under the session's own temp directory (test binaries run
// under a scrubbed env), and by descent from either.
package sessionreap

import (
	"path/filepath"
	"strings"
	"time"
)

// MarkerEnv is the environment variable every session process inherits.
const MarkerEnv = "TALLPINE_SESSION_ID"

// UserShellEnv marks a shell terminal the user opened on a session, and
// everything run from it: the user's, never reaped.
const UserShellEnv = "TALLPINE_SHELL_TERMINAL"

// Proc is one process as the snapshot saw it.
type Proc struct {
	PID   int
	PPID  int
	Start time.Time // zero when unknown
	Exe   string    // executable path, "" when unknown
	// Marker is the process's TALLPINE_SESSION_ID, "" when it has none or its
	// environment could not be read.
	Marker string
	// UserShell is set for a user shell terminal and its descendants.
	UserShell bool
}

// Match says which session to reap.
type Match struct {
	SessionID string
	// Dirs are directories private to the session (its temp dir): a process
	// whose executable lives below one belongs to the session.
	Dirs []string
	// StartedBefore, when set, spares processes started at or after it, so a
	// session restored under the same id keeps its new processes.
	StartedBefore time.Time
}

// Select returns the pids to stop for m. self is the calling process: it and
// its ancestors are never selected, nor is pid 1. A process tagged to another
// session is never selected, and neither is any process with such a process
// below it (shared infrastructure such as a tmux server), nor a user shell
// terminal or anything run from it.
func Select(procs []Proc, m Match, self int) []int {
	if m.SessionID == "" {
		return nil
	}
	byPID := make(map[int]Proc, len(procs))
	children := make(map[int][]int, len(procs))
	for _, p := range procs {
		byPID[p.PID] = p
		children[p.PPID] = append(children[p.PPID], p.PID)
	}
	protected := map[int]bool{0: true, 1: true}
	for pid, seen := self, 0; pid > 1 && seen < len(procs)+1; seen++ {
		protected[pid] = true
		p, ok := byPID[pid]
		if !ok {
			break
		}
		pid = p.PPID
	}
	foreign := func(p Proc) bool { return p.Marker != "" && p.Marker != m.SessionID }
	// hostsForeign: p or a descendant is tagged to another session.
	hosts := map[int]bool{}
	var hostsForeign func(pid int, depth int) bool
	hostsForeign = func(pid int, depth int) bool {
		if v, ok := hosts[pid]; ok {
			return v
		}
		hosts[pid] = false // cycle guard
		v := foreign(byPID[pid])
		if depth < 64 {
			for _, c := range children[pid] {
				if hostsForeign(c, depth+1) {
					v = true
				}
			}
		}
		hosts[pid] = v
		return v
	}
	eligible := func(p Proc) bool {
		if protected[p.PID] || p.UserShell || foreign(p) || hostsForeign(p.PID, 0) {
			return false
		}
		return m.StartedBefore.IsZero() || p.Start.IsZero() || p.Start.Before(m.StartedBefore)
	}
	selected := map[int]bool{}
	var queue []int
	for _, p := range procs {
		if (p.Marker == m.SessionID || under(p.Exe, m.Dirs)) && eligible(p) {
			selected[p.PID] = true
			queue = append(queue, p.PID)
		}
	}
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		for _, c := range children[pid] {
			if !selected[c] && eligible(byPID[c]) {
				selected[c] = true
				queue = append(queue, c)
			}
		}
	}
	out := make([]int, 0, len(selected))
	for _, p := range procs {
		if selected[p.PID] {
			out = append(out, p.PID)
		}
	}
	return out
}

func under(path string, dirs []string) bool {
	if path == "" {
		return false
	}
	for _, d := range dirs {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		forms := []string{filepath.Clean(d)}
		if resolved, err := filepath.EvalSymlinks(d); err == nil {
			forms = append(forms, resolved)
		}
		for _, f := range forms {
			if strings.HasPrefix(filepath.Clean(path), f+string(filepath.Separator)) {
				return true
			}
		}
	}
	return false
}
