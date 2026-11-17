// Package machineload decides whether the machine can take one more native
// reviewer. Every reviewer pane runs a model client plus the tests it kicks
// off, so several at once on a busy laptop make everything crawl. The gate
// holds a new pass back while the one-minute load is at or above the
// configured share of the cores, or while the machine-wide count of running
// reviewers has reached the cap.
package machineload

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"

	"tallpine.eu/tallpine/backend/internal/procmem"
)

// Defaults of the two limits; 0 disables a limit.
const (
	DefaultMaxReviewers = 3
	DefaultLoadPercent  = 100
)

// Limits are the two thresholds. A value of 0 turns that check off.
type Limits struct {
	// MaxReviewers is the most reviewers with a running pass, across every
	// project, before a new pass is held.
	MaxReviewers int
	// LoadPercent is the one-minute load, as a percentage of the cores, at
	// which a new pass is held.
	LoadPercent int
}

// DefaultLimits are the limits of a machine whose owner changed nothing.
func DefaultLimits() Limits {
	return Limits{MaxReviewers: DefaultMaxReviewers, LoadPercent: DefaultLoadPercent}
}

// Status is one reading of the machine against the limits.
type Status struct {
	Busy bool
	// ByLoad and ByCount say which limit held the pass.
	ByLoad, ByCount bool
	// Load1 is the one-minute load, or -1 when this platform has none.
	Load1 float64
	Cores int
	// Reviewers is the number of running reviewers, including passes admitted
	// but not yet recorded; MaxReviewers is the cap (0: off).
	Reviewers    int
	MaxReviewers int
	LoadPercent  int
}

// Reason is the one-line explanation a held pass and a busy spawn carry.
func (s Status) Reason() string {
	load := "n/a"
	if s.Load1 >= 0 {
		load = fmt.Sprintf("%.1f", s.Load1)
	}
	return fmt.Sprintf("machine busy (load %s/%d cores, reviewers %d/%d)", load, s.Cores, s.Reviewers, s.MaxReviewers)
}

// Deps are the gate's inputs. Each is read once per decision.
type Deps struct {
	// Limits returns the effective limits (settings with env overrides).
	Limits func(ctx context.Context) (Limits, error)
	// Reviewers counts the reviewers with a running pass on the machine.
	Reviewers func(ctx context.Context) (int, error)
	// ReadSystem reads the host; nil uses procmem.ReadSystem.
	ReadSystem func() (procmem.System, error)
}

// Gate answers Check and Admit. The zero value is not usable; use New.
type Gate struct {
	deps Deps

	mu       sync.Mutex
	inflight int
}

// New builds a gate from deps.
func New(deps Deps) *Gate {
	if deps.ReadSystem == nil {
		deps.ReadSystem = procmem.ReadSystem
	}
	return &Gate{deps: deps}
}

// Check reads the machine once and reports whether it is busy. On an error
// the returned Status is the zero value (not busy): callers fail open.
func (g *Gate) Check(ctx context.Context) (Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.checkLocked(ctx)
}

// Admit is Check that also reserves a reviewer slot when the machine is not
// busy, so concurrent callers cannot all pass a cap of one. The caller calls
// release once its pass is recorded as running (the count then comes from the
// store). release is never nil and is safe to call twice. On an error nothing
// is reserved and the caller proceeds.
func (g *Gate) Admit(ctx context.Context) (func(), Status, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	status, err := g.checkLocked(ctx)
	if err != nil || status.Busy {
		return func() {}, status, err
	}
	g.inflight++
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.inflight--
			g.mu.Unlock()
		})
	}, status, nil
}

func (g *Gate) checkLocked(ctx context.Context) (Status, error) {
	limits, err := g.deps.Limits(ctx)
	if err != nil {
		return Status{}, fmt.Errorf("machine load limits: %w", err)
	}
	status := Status{Load1: -1, MaxReviewers: limits.MaxReviewers, LoadPercent: limits.LoadPercent, Cores: runtime.NumCPU()}
	if limits.MaxReviewers > 0 {
		running, err := g.deps.Reviewers(ctx)
		if err != nil {
			return Status{}, fmt.Errorf("machine load reviewers: %w", err)
		}
		status.Reviewers = running + g.inflight
		status.ByCount = status.Reviewers >= limits.MaxReviewers
	}
	if limits.LoadPercent > 0 {
		sys, err := g.deps.ReadSystem()
		switch {
		case errors.Is(err, procmem.ErrUnsupported):
			// No load on this platform: only the count cap applies.
		case err != nil:
			return Status{}, fmt.Errorf("machine load: %w", err)
		default:
			if sys.CPUCount > 0 {
				status.Cores = sys.CPUCount
			}
			status.Load1 = sys.Load1
			// A platform with no load reports -1, which is never at the threshold.
			status.ByLoad = sys.Load1*100 >= float64(status.Cores*limits.LoadPercent)
		}
	}
	status.Busy = status.ByLoad || status.ByCount
	return status, nil
}
