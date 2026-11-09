package sessionreap

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

// startSlop absorbs the jitter between two readings of one process's start
// time: Linux derives it from a whole-second boot time that clock steps can
// move, so a shift of exactly this much must still match. A larger step only
// makes the proof fail, which spares the process.
const startSlop = time.Second

// ErrOwnerUnknown reports that a process was spared only because its working
// directory could not be read, so a later reap may still need to stop it.
var ErrOwnerUnknown = errors.New("sessionreap: could not read a candidate's working directory")

// Orphan is a provider process tree recorded by a chat host that is now proven
// dead. Nothing is left to stop that provider: a hung one never sees stdin EOF.
type Orphan struct {
	// PID is the provider, which led its own process group.
	PID int
	// Start is its recorded start time, zero when it could not be read.
	Start time.Time
	// Marker is the TALLPINE_SESSION_ID the provider was launched with.
	Marker string
	// Spare lists directories to leave alone: a process whose executable or
	// working directory is below one is never stopped.
	Spare []string
}

// SelectOrphan returns the pids proven to belong to o, without the Spare
// check. A process is proven by the recorded pid and start time, or by
// carrying o's marker in o's process group. A pid reused by an unrelated
// process has neither, so it is never selected. Selection then follows the
// live provider's group and descendants under the same guards as Select.
func SelectOrphan(procs []Proc, o Orphan, self int) []int {
	if o.PID <= 1 {
		return nil
	}
	rootLive := false
	for _, p := range procs {
		if p.PID == o.PID && SameStart(p.Start, o.Start) {
			rootLive = true
			break
		}
	}
	ix := newProcIndex(procs, self)
	eligible := ix.guard(o.Marker)
	var seeds []int
	for _, p := range procs {
		// The group id is the provider's only while it lives, or while members
		// it left behind keep the group from being recycled.
		inTree := p.PID == o.PID || p.PGID == o.PID
		if inTree && (rootLive || markerProven(p, o)) && eligible(p) {
			seeds = append(seeds, p.PID)
		}
	}
	return ix.expand(procs, seeds, eligible)
}

// SameStart reports whether two readings are one process's start time. Unknown
// readings never match.
func SameStart(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	d := a.Sub(b)
	return d >= -startSlop && d <= startSlop
}

// markerProven reports a process launched with o's session marker, in o's
// group, and not older than the recorded provider.
func markerProven(p Proc, o Orphan) bool {
	if o.Marker == "" || p.Marker != o.Marker || p.PGID != o.PID {
		return false
	}
	return o.Start.IsZero() || p.Start.IsZero() || !p.Start.Before(o.Start.Add(-startSlop))
}

// ReapOrphans stops every orphan's proven processes, sharing one grace so a
// boot with many orphans is not delayed once per orphan.
func (r *Reaper) ReapOrphans(ctx context.Context, orphans []Orphan) (int, error) {
	if len(orphans) == 0 {
		return 0, nil
	}
	procs, err := r.list()
	if err != nil {
		return 0, err
	}
	verdicts := map[procKey]spareVerdict{}
	// The proof of a tree dies with its root, and a provider that exits on
	// SIGTERM must not free the children still ignoring it. An exact, known
	// start keeps a pid reused during the grace out of this set.
	proven := map[procKey]bool{}
	n, err := r.stop(ctx, procs, func(snapshot []Proc) []int {
		byPID := make(map[int]Proc, len(snapshot))
		for _, p := range snapshot {
			byPID[p.PID] = p
		}
		seen := map[int]bool{}
		var out []int
		for _, o := range orphans {
			for _, pid := range SelectOrphan(snapshot, o, r.self) {
				if !seen[pid] && !r.isSpared(ctx, byPID[pid], o.Spare, verdicts) {
					seen[pid] = true
					out = append(out, pid)
					if start := byPID[pid].Start; !start.IsZero() {
						proven[procKey{pid, start}] = true
					}
				}
			}
		}
		for _, p := range snapshot {
			if !seen[p.PID] && proven[procKey{p.PID, p.Start}] {
				seen[p.PID] = true
				out = append(out, p.PID)
			}
		}
		return out
	})
	if err != nil {
		return n, err
	}
	for _, v := range verdicts {
		if v.unknown {
			return n, ErrOwnerUnknown
		}
	}
	return n, nil
}

type procKey struct {
	pid   int
	start time.Time
}

type spareVerdict struct {
	spared  bool
	unknown bool // spared only because the working directory was unreadable
}

// isSpared reports whether p sits in a spared directory. A working directory
// that cannot be read counts as spared: being unable to tell must never kill.
// The answer is cached per process because the grace loop re-selects.
func (r *Reaper) isSpared(ctx context.Context, p Proc, dirs []string, cache map[procKey]spareVerdict) bool {
	if len(dirs) == 0 {
		return false
	}
	key := procKey{p.PID, p.Start}
	if v, ok := cache[key]; ok {
		return v.spared
	}
	v := spareVerdict{spared: under(p.Exe, dirs)}
	if !v.spared {
		cwd, ok := r.cwd(ctx, p.PID)
		// Joined so a process running in a spared directory itself also counts.
		v = spareVerdict{spared: !ok || under(filepath.Join(cwd, "_"), dirs), unknown: !ok}
	}
	cache[key] = v
	return v.spared
}
