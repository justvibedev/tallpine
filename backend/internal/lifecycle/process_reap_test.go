package lifecycle

import (
	"context"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/sessionreap"
)

type fakeProcessReaper struct {
	mu    sync.Mutex
	procs []sessionreap.Proc
	// later, when set, is what every snapshot after the first returns.
	later   []sessionreap.Proc
	snaps   int
	gotPIDs [][]int
	matches []sessionreap.Match
	done    chan struct{}
}

func (f *fakeProcessReaper) Snapshot() ([]sessionreap.Proc, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps++
	if f.snaps > 1 && f.later != nil {
		return f.later, nil
	}
	return f.procs, nil
}

func (f *fakeProcessReaper) ReapFrom(_ context.Context, procs []sessionreap.Proc, m sessionreap.Match) (int, error) {
	f.mu.Lock()
	f.matches = append(f.matches, m)
	var pids []int
	for _, p := range procs {
		pids = append(pids, p.PID)
	}
	f.gotPIDs = append(f.gotPIDs, pids)
	f.mu.Unlock()
	if f.done != nil {
		f.done <- struct{}{}
	}
	return 1, nil
}

func (f *fakeProcessReaper) reaped() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, m := range f.matches {
		ids = append(ids, m.SessionID)
	}
	sort.Strings(ids)
	return ids
}

func TestMarkTerminatedReapsSessionProcesses(t *testing.T) {
	m, st, _ := newManager()
	dataDir := t.TempDir()
	m.dataDir = dataDir
	reaper := &fakeProcessReaper{done: make(chan struct{}, 1)}
	m.processes = reaper
	st.sessions["mer-1"] = working("mer-1")
	before := time.Now()
	if err := m.MarkTerminated(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaper.done:
	case <-time.After(5 * time.Second):
		t.Fatal("MarkTerminated did not reap the session's processes")
	}
	got := reaper.matches[0]
	if got.SessionID != "mer-1" || got.StartedBefore.Before(before) {
		t.Fatalf("match = %+v", got)
	}
	if want := filepath.Join(dataDir, "tmp", "mer-1"); len(got.Dirs) != 1 || got.Dirs[0] != want {
		t.Fatalf("dirs = %v, want [%s]", got.Dirs, want)
	}
}

func TestSweepEndedSessionProcessesOnlyReapsSettledEndedSessions(t *testing.T) {
	m, st, _ := newManager()
	dataDir := t.TempDir()
	m.dataDir = dataDir
	old := time.Now().Add(-time.Hour)
	ended := working("ended")
	ended.IsTerminated, ended.UpdatedAt = true, old
	recent := working("recent")
	recent.IsTerminated, recent.UpdatedAt = true, time.Now()
	live := working("live")
	live.UpdatedAt = old
	scrubbed := working("scrubbed")
	scrubbed.IsTerminated, scrubbed.UpdatedAt = true, old
	st.sessions["ended"], st.sessions["recent"], st.sessions["live"], st.sessions["scrubbed"] = ended, recent, live, scrubbed
	reaper := &fakeProcessReaper{procs: []sessionreap.Proc{
		{PID: 10, Marker: "ended"},
		{PID: 11, Marker: "ended"},
		{PID: 12, Marker: "recent"},
		{PID: 13, Marker: "live"},
		{PID: 14, Marker: "unknown"},
		{PID: 15, Exe: filepath.Join(dataDir, "tmp", "scrubbed", "go-build1", "pkg.test")},
		{PID: 16, Exe: filepath.Join(dataDir, "tmp", "unknown2", "x")},
	}}
	m.processes = reaper
	m.SweepEndedSessionProcesses(context.Background())
	got := reaper.reaped()
	if len(got) != 2 || got[0] != "ended" || got[1] != "scrubbed" {
		t.Fatalf("swept %v, want [ended scrubbed]", got)
	}
}

// The sweep's snapshot only names candidates: each session is reaped from a
// fresh snapshot, so a pid that exited (and may be reused) is never signalled.
func TestSweepReapsEachSessionFromAFreshSnapshot(t *testing.T) {
	m, st, _ := newManager()
	ended := working("ended")
	ended.IsTerminated, ended.UpdatedAt = true, time.Now().Add(-time.Hour)
	st.sessions["ended"] = ended
	reaper := &fakeProcessReaper{
		procs: []sessionreap.Proc{{PID: 10, Marker: "ended"}, {PID: 11, Marker: "ended"}},
		later: []sessionreap.Proc{{PID: 11, Marker: "ended"}},
	}
	m.processes = reaper
	m.SweepEndedSessionProcesses(context.Background())
	if len(reaper.gotPIDs) != 1 || len(reaper.gotPIDs[0]) != 1 || reaper.gotPIDs[0][0] != 11 {
		t.Fatalf("reaped from %v, want the fresh snapshot [[11]]", reaper.gotPIDs)
	}
	// Processes started after the session ended (a preview or shell the user
	// opened on it) are not its leftovers.
	if got := reaper.matches[0].StartedBefore; !got.Equal(ended.UpdatedAt) {
		t.Fatalf("sweep cutoff = %v, want the session end %v", got, ended.UpdatedAt)
	}
}
