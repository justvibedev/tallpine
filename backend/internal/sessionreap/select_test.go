package sessionreap

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

func sorted(p []int) []int { sort.Ints(p); return p }

func TestSelect(t *testing.T) {
	tmp := t.TempDir()
	scratch := filepath.Join(tmp, "s-1")
	t0 := time.Unix(1_000_000, 0)
	procs := []Proc{
		{PID: 1, PPID: 0},
		{PID: 10, PPID: 1, Marker: ""},                                          // the daemon's parent (launchd child)
		{PID: 100, PPID: 10, Marker: "s-1"},                                     // daemon itself, tagged (started from a session)
		{PID: 200, PPID: 100, Marker: "s-1"},                                    // harness the daemon spawned
		{PID: 201, PPID: 200, Marker: ""},                                       // child that scrubbed its env
		{PID: 300, PPID: 1, Marker: "s-1"},                                      // detached server, reparented
		{PID: 301, PPID: 300},                                                   // its worker
		{PID: 400, PPID: 1, Marker: "s-2"},                                      // other session's server
		{PID: 401, PPID: 400, Marker: "s-1"},                                    // tagged s-1 but under s-2: s-2's tree keeps it? no: selected (own marker)
		{PID: 500, PPID: 1, Marker: "s-1"},                                      // tmux-like host started from s-1 ...
		{PID: 501, PPID: 500, Marker: "s-2"},                                    // ... hosting s-2: spare the host
		{PID: 600, PPID: 1, Exe: filepath.Join(scratch, "go-build1", "x.test")}, // test binary, scrubbed env
		{PID: 601, PPID: 600},                                                   // its pty host child
		{PID: 700, PPID: 1, Exe: filepath.Join(tmp, "s-10", "bin")},             // sibling dir with shared prefix
		{PID: 800, PPID: 1, Marker: "s-10"},                                     // id with shared prefix
		{PID: 900, PPID: 1, Marker: "s-1", Start: t0.Add(time.Minute)},          // started after the cutoff (restored session)
		{PID: 901, PPID: 1, Marker: "s-1", Start: t0.Add(-time.Minute)},
		{PID: 950, PPID: 1, Marker: "s-1", UserShell: true},   // user shell terminal on s-1
		{PID: 951, PPID: 950, Marker: "s-1", UserShell: true}, // command the user runs in it
	}
	m := Match{SessionID: "s-1", Dirs: []string{scratch}, StartedBefore: t0}
	got := sorted(Select(procs, m, 100))
	want := []int{200, 201, 300, 301, 401, 600, 601, 901}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Select = %v, want %v", got, want)
	}
}

func TestSelectNeverSelectsSelfOrAncestors(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, Marker: "s-1"},
		{PID: 50, PPID: 1, Marker: "s-1"},
		{PID: 60, PPID: 50, Marker: "s-1"},
		{PID: 70, PPID: 60, Marker: "s-1"},
	}
	if got := Select(procs, Match{SessionID: "s-1"}, 60); !reflect.DeepEqual(got, []int{70}) {
		t.Fatalf("Select = %v, want [70]", got)
	}
}

func TestSelectEmptySessionSelectsNothing(t *testing.T) {
	procs := []Proc{{PID: 5, PPID: 1}, {PID: 6, PPID: 1, Marker: ""}}
	if got := Select(procs, Match{}, 2); len(got) != 0 {
		t.Fatalf("Select = %v, want none", got)
	}
}

func TestMarkerFrom(t *testing.T) {
	raw := [][]byte{[]byte("A=1"), []byte("TALLPINE_SESSION_ID=s-1"), []byte("")}
	if got := markerFrom(raw); got != "s-1" {
		t.Fatalf("markerFrom = %q", got)
	}
	if got := markerFrom([][]byte{[]byte("X=TALLPINE_SESSION_ID=s-1")}); got != "" {
		t.Fatalf("markerFrom matched inside a value: %q", got)
	}
	if _, shell := envFacts([][]byte{[]byte("TALLPINE_SHELL_TERMINAL=1")}); !shell {
		t.Fatal("envFacts missed the user shell marker")
	}
	if _, shell := envFacts([][]byte{[]byte("TALLPINE_SHELL_TERMINAL=")}); shell {
		t.Fatal("envFacts took an empty user shell marker as set")
	}
}
