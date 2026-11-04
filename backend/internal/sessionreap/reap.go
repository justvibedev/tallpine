package sessionreap

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// DefaultGrace is how long processes get to exit after SIGTERM.
const DefaultGrace = 3 * time.Second

// ErrUnsupported reports a platform the snapshot cannot read.
var ErrUnsupported = errors.New("sessionreap: process listing unsupported on this platform")

// Reaper stops a session's leftover processes.
type Reaper struct {
	list   func() ([]Proc, error)
	signal func(pid int, sig syscall.Signal) error
	self   int
	grace  time.Duration
}

// New returns a Reaper over the system's process table.
func New() *Reaper {
	return &Reaper{list: Snapshot, signal: signalPID, self: os.Getpid(), grace: DefaultGrace}
}

// Snapshot lists the processes the caller may signal.
func (r *Reaper) Snapshot() ([]Proc, error) { return r.list() }

// Reap stops m's processes: SIGTERM, up to the grace for them to exit, then
// SIGKILL for whatever is still selected on a fresh snapshot (which also
// catches children spawned in the meantime). It returns how many processes it
// signalled.
func (r *Reaper) Reap(ctx context.Context, m Match) (int, error) {
	procs, err := r.list()
	if err != nil {
		return 0, err
	}
	return r.reapFrom(ctx, procs, m)
}

// ReapFrom is Reap over a snapshot the caller already took.
func (r *Reaper) ReapFrom(ctx context.Context, procs []Proc, m Match) (int, error) {
	return r.reapFrom(ctx, procs, m)
}

func (r *Reaper) reapFrom(ctx context.Context, procs []Proc, m Match) (int, error) {
	pids := Select(procs, m, r.self)
	if len(pids) == 0 {
		return 0, nil
	}
	for _, pid := range pids {
		_ = r.signal(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(r.grace)
	for {
		left, err := r.list()
		if err != nil {
			return len(pids), err
		}
		remaining := Select(left, m, r.self)
		if len(remaining) == 0 {
			return len(pids), nil
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			for _, pid := range remaining {
				_ = r.signal(pid, syscall.SIGKILL)
			}
			return len(pids) + countNew(pids, remaining), nil
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func countNew(before, after []int) int {
	seen := make(map[int]bool, len(before))
	for _, p := range before {
		seen[p] = true
	}
	n := 0
	for _, p := range after {
		if !seen[p] {
			n++
		}
	}
	return n
}
