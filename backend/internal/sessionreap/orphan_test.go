package sessionreap

import (
	"context"
	"errors"
	"reflect"
	"syscall"
	"testing"
	"time"
)

var (
	orphanStart = time.Unix(1_000_000, 0)
	otherStart  = orphanStart.Add(time.Hour)
)

func TestSelectOrphan(t *testing.T) {
	orphan := Orphan{PID: 100, Start: orphanStart, Marker: "s1"}
	tests := []struct {
		name  string
		procs []Proc
		o     Orphan
		want  []int
	}{
		{
			name: "recorded pid and start prove an unmarked provider and its tree",
			procs: []Proc{
				{PID: 100, PPID: 1, PGID: 100, Start: orphanStart},
				{PID: 101, PPID: 100, PGID: 100, Start: otherStart},
				{PID: 102, PPID: 1, PGID: 100, Start: otherStart},
			},
			o: orphan, want: []int{100, 101, 102},
		},
		{
			name:  "reused pid with another start and no marker is never selected",
			procs: []Proc{{PID: 100, PPID: 50, PGID: 100, Start: otherStart}, {PID: 101, PPID: 100, PGID: 100, Start: otherStart}},
			o:     orphan,
		},
		{
			name:  "reused pid is spared even when no start was recorded",
			procs: []Proc{{PID: 100, PPID: 50, PGID: 100, Start: otherStart}},
			o:     Orphan{PID: 100, Marker: "s1"},
		},
		{
			name:  "reused pid carrying another session's marker is spared",
			procs: []Proc{{PID: 100, PPID: 1, PGID: 100, Start: orphanStart, Marker: "s2"}},
			o:     orphan,
		},
		{
			name: "marker in the group proves members a dead wrapper left behind",
			procs: []Proc{
				{PID: 200, PPID: 1, PGID: 100, Start: otherStart, Marker: "s1"},
				{PID: 201, PPID: 1, PGID: 100, Start: otherStart},
				{PID: 202, PPID: 1, PGID: 300, Start: otherStart, Marker: "s1"},
			},
			o: orphan, want: []int{200},
		},
		{
			name:  "same-session process at a reused pid outside the group is spared",
			procs: []Proc{{PID: 100, PPID: 1, PGID: 300, Start: otherStart, Marker: "s1"}},
			o:     orphan,
		},
		{
			name:  "marked process older than the recorded provider is spared",
			procs: []Proc{{PID: 200, PPID: 1, PGID: 100, Start: orphanStart.Add(-time.Hour), Marker: "s1"}},
			o:     orphan,
		},
		{
			name: "a user shell below the provider is spared",
			procs: []Proc{
				{PID: 100, PPID: 1, PGID: 100, Start: orphanStart},
				{PID: 101, PPID: 100, PGID: 100, Start: otherStart, UserShell: true},
				{PID: 102, PPID: 100, PGID: 100, Start: otherStart},
			},
			o: orphan, want: []int{100, 102},
		},
		{
			name: "a provider hosting another session's process is spared whole",
			procs: []Proc{
				{PID: 100, PPID: 1, PGID: 100, Start: orphanStart},
				{PID: 102, PPID: 100, PGID: 100, Start: otherStart},
				{PID: 103, PPID: 102, PGID: 100, Start: otherStart, Marker: "s2"},
			},
			o: orphan,
		},
		{
			name:  "pid one is never selected",
			procs: []Proc{{PID: 1, PGID: 1, Start: orphanStart}},
			o:     Orphan{PID: 1, Start: orphanStart},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sorted(SelectOrphan(tt.procs, tt.o, 9999))
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SelectOrphan = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReapOrphansSparesLocalCI(t *testing.T) {
	const spare = "/home/u/.tallpine/local-ci"
	tests := []struct {
		name    string
		exe     string
		cwd     string
		cwdOK   bool
		signals int
		unknown bool
	}{
		{name: "executable under local-ci", exe: spare + "/gocache/app.test", cwd: "/work", cwdOK: true},
		{name: "working directory under local-ci", exe: "/usr/local/go/bin/go", cwd: spare + "/work/main", cwdOK: true},
		{name: "working directory is local-ci itself", exe: "/usr/local/go/bin/go", cwd: spare, cwdOK: true},
		{name: "unreadable working directory", exe: "/usr/local/go/bin/go", unknown: true},
		{name: "unrelated location is reaped", exe: "/usr/local/bin/agent", cwd: "/work", cwdOK: true, signals: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := Proc{PID: 100, PPID: 1, PGID: 100, Start: orphanStart, Exe: tt.exe}
			gone := false
			signals := 0
			r := &Reaper{
				list: func() ([]Proc, error) {
					if gone {
						return nil, nil
					}
					return []Proc{proc}, nil
				},
				signal: func(int, syscall.Signal) error { signals++; gone = true; return nil },
				cwd:    func(context.Context, int) (string, bool) { return tt.cwd, tt.cwdOK },
				self:   9999, grace: time.Millisecond,
			}
			orphan := Orphan{PID: 100, Start: orphanStart, Spare: []string{spare}}
			_, err := r.ReapOrphans(context.Background(), []Orphan{orphan})
			if tt.unknown != errors.Is(err, ErrOwnerUnknown) || (!tt.unknown && err != nil) {
				t.Fatalf("err = %v, want unknown=%v", err, tt.unknown)
			}
			if signals != tt.signals {
				t.Fatalf("signals = %d, want %d", signals, tt.signals)
			}
		})
	}
}

func TestReapOrphansRechecksProofBeforeKill(t *testing.T) {
	// The provider ignores SIGTERM and exits during the grace; its pid is then
	// reused by an unrelated process, which must not get the SIGKILL.
	calls := 0
	var sigs []syscall.Signal
	r := &Reaper{
		list: func() ([]Proc, error) {
			calls++
			if calls == 1 {
				return []Proc{{PID: 100, PPID: 1, PGID: 100, Start: orphanStart}}, nil
			}
			return []Proc{{PID: 100, PPID: 50, PGID: 100, Start: otherStart}}, nil
		},
		signal: func(_ int, s syscall.Signal) error { sigs = append(sigs, s); return nil },
		cwd:    func(context.Context, int) (string, bool) { return "/work", true },
		self:   9999, grace: 0,
	}
	if _, err := r.ReapOrphans(context.Background(), []Orphan{{PID: 100, Start: orphanStart}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sigs, []syscall.Signal{syscall.SIGTERM}) {
		t.Fatalf("signals = %v, want only the first SIGTERM", sigs)
	}
}

func TestSameStart(t *testing.T) {
	tests := []struct {
		name string
		a, b time.Time
		want bool
	}{
		{"equal", orphanStart, orphanStart, true},
		{"exactly one second later", orphanStart.Add(time.Second), orphanStart, true},
		{"exactly one second earlier", orphanStart.Add(-time.Second), orphanStart, true},
		{"just over one second later", orphanStart.Add(time.Second + time.Nanosecond), orphanStart, false},
		{"just over one second earlier", orphanStart.Add(-time.Second - time.Nanosecond), orphanStart, false},
		{"unknown reading", time.Time{}, orphanStart, false},
		{"both unknown", time.Time{}, time.Time{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameStart(tt.a, tt.b); got != tt.want {
				t.Fatalf("SameStart = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReapOrphansKillsTreeAfterProviderExitsOnTERM(t *testing.T) {
	// The provider dies on SIGTERM. An unmarked group member and a setsid
	// descendant ignore it, and have no proof left of their own afterwards.
	before := []Proc{
		{PID: 100, PPID: 1, PGID: 100, Start: orphanStart},
		{PID: 101, PPID: 100, PGID: 100, Start: otherStart},
		{PID: 102, PPID: 100, PGID: 102, Start: otherStart},
	}
	after := []Proc{
		{PID: 101, PPID: 1, PGID: 100, Start: otherStart},
		{PID: 102, PPID: 1, PGID: 102, Start: otherStart},
		// Never seen while the provider lived, so it was never proven.
		{PID: 103, PPID: 1, PGID: 100, Start: otherStart},
	}
	termed := false
	killed := map[int]bool{}
	r := &Reaper{
		list: func() ([]Proc, error) {
			if termed {
				return after, nil
			}
			return before, nil
		},
		signal: func(pid int, s syscall.Signal) error {
			if s == syscall.SIGKILL {
				killed[pid] = true
			} else {
				termed = true
			}
			return nil
		},
		self: 9999, grace: 0,
	}
	if _, err := r.ReapOrphans(context.Background(), []Orphan{{PID: 100, Start: orphanStart}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(killed, map[int]bool{101: true, 102: true}) {
		t.Fatalf("SIGKILLed %v, want the two proven survivors only", killed)
	}
}
