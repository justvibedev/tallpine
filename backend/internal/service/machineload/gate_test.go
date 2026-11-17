package machineload

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"tallpine.eu/tallpine/backend/internal/procmem"
)

func newGate(limits Limits, reviewers int, load float64, cores int) *Gate {
	return New(Deps{
		Limits:     func(context.Context) (Limits, error) { return limits, nil },
		Reviewers:  func(context.Context) (int, error) { return reviewers, nil },
		ReadSystem: func() (procmem.System, error) { return procmem.System{CPUCount: cores, Load1: load}, nil },
	})
}

func TestCheckBusyByLoad(t *testing.T) {
	st, err := newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 0, 8.5, 8).Check(context.Background())
	if err != nil || !st.Busy || !st.ByLoad || st.ByCount {
		t.Fatalf("status = %+v, err = %v; want busy by load only", st, err)
	}
	if got := st.Reason(); !strings.Contains(got, "load 8.5/8 cores") || !strings.Contains(got, "reviewers 0/3") {
		t.Fatalf("reason = %q", got)
	}
	st, _ = newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 0, 7.9, 8).Check(context.Background())
	if st.Busy {
		t.Fatalf("load 7.9 of 8 cores is busy: %+v", st)
	}
}

func TestCheckLoadPercentScalesThreshold(t *testing.T) {
	st, _ := newGate(Limits{LoadPercent: 50}, 0, 4, 8).Check(context.Background())
	if !st.ByLoad {
		t.Fatalf("load 4 on 8 cores at 50%% must be busy: %+v", st)
	}
}

func TestCheckBusyByCount(t *testing.T) {
	st, err := newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 3, 0.1, 8).Check(context.Background())
	if err != nil || !st.Busy || !st.ByCount || st.ByLoad {
		t.Fatalf("status = %+v, err = %v; want busy by count only", st, err)
	}
	st, _ = newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 2, 0.1, 8).Check(context.Background())
	if st.Busy {
		t.Fatalf("2 of 3 reviewers is busy: %+v", st)
	}
}

func TestZeroDisablesEachLimit(t *testing.T) {
	st, _ := newGate(Limits{MaxReviewers: 0, LoadPercent: 100}, 99, 0.1, 8).Check(context.Background())
	if st.Busy {
		t.Fatalf("a zero reviewer cap must not hold: %+v", st)
	}
	st, _ = newGate(Limits{MaxReviewers: 3, LoadPercent: 0}, 0, 99, 8).Check(context.Background())
	if st.Busy {
		t.Fatalf("a zero load percent must not hold: %+v", st)
	}
}

func TestWindowsLoadMinusOneOnlyCountApplies(t *testing.T) {
	g := newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 1, -1, 8)
	st, err := g.Check(context.Background())
	if err != nil || st.Busy || !strings.Contains(st.Reason(), "load n/a") {
		t.Fatalf("status = %+v, err = %v; want not busy with load n/a", st, err)
	}
	st, _ = newGate(Limits{MaxReviewers: 3, LoadPercent: 100}, 3, -1, 8).Check(context.Background())
	if !st.ByCount {
		t.Fatalf("the count cap must still apply: %+v", st)
	}
}

func TestUnsupportedPlatformOnlyCountApplies(t *testing.T) {
	g := New(Deps{
		Limits:     func(context.Context) (Limits, error) { return DefaultLimits(), nil },
		Reviewers:  func(context.Context) (int, error) { return 0, nil },
		ReadSystem: func() (procmem.System, error) { return procmem.System{}, procmem.ErrUnsupported },
	})
	if st, err := g.Check(context.Background()); err != nil || st.Busy {
		t.Fatalf("status = %+v, err = %v", st, err)
	}
}

func TestFailsOpenOnReadErrors(t *testing.T) {
	boom := errors.New("boom")
	cases := map[string]Deps{
		"limits":    {Limits: func(context.Context) (Limits, error) { return Limits{}, boom }},
		"reviewers": {Limits: func(context.Context) (Limits, error) { return DefaultLimits(), nil }, Reviewers: func(context.Context) (int, error) { return 99, boom }},
		"system": {
			Limits:     func(context.Context) (Limits, error) { return DefaultLimits(), nil },
			Reviewers:  func(context.Context) (int, error) { return 0, nil },
			ReadSystem: func() (procmem.System, error) { return procmem.System{Load1: 99, CPUCount: 1}, boom },
		},
	}
	for name, deps := range cases {
		release, st, err := New(deps).Admit(context.Background())
		if err == nil || st.Busy || release == nil {
			t.Fatalf("%s: status = %+v, err = %v; want a not-busy status with the error", name, st, err)
		}
		release()
	}
}

func TestConcurrentAdmitRespectsCap(t *testing.T) {
	g := newGate(Limits{MaxReviewers: 3}, 1, 0, 8)
	var wg sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, st, err := g.Admit(context.Background())
			if err == nil && !st.Busy {
				mu.Lock()
				admitted++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if admitted != 2 {
		t.Fatalf("admitted %d with 1 running and a cap of 3, want 2", admitted)
	}
}

func TestReleaseFreesTheSlotOnce(t *testing.T) {
	g := newGate(Limits{MaxReviewers: 1}, 0, 0, 8)
	release, st, _ := g.Admit(context.Background())
	if st.Busy {
		t.Fatal("first admit held")
	}
	if _, st, _ := g.Admit(context.Background()); !st.Busy {
		t.Fatal("second admit passed a cap of one")
	}
	release()
	release()
	if _, st, _ := g.Admit(context.Background()); st.Busy {
		t.Fatal("slot not freed by release")
	}
}

func TestWithEnvOverridesOnlyValidValues(t *testing.T) {
	env := map[string]string{EnvMaxReviewers: "7", EnvLoadPercent: "garbage"}
	got := DefaultLimits().WithEnv(func(name string) (string, bool) { v, ok := env[name]; return v, ok })
	if got.MaxReviewers != 7 || got.LoadPercent != DefaultLoadPercent {
		t.Fatalf("limits = %+v, want 7 reviewers and the stored load percent", got)
	}
	env[EnvMaxReviewers] = "0"
	if got := DefaultLimits().WithEnv(func(name string) (string, bool) { v, ok := env[name]; return v, ok }); got.MaxReviewers != 0 {
		t.Fatalf("env 0 must disable the cap, got %+v", got)
	}
	if got := DefaultLimits().WithEnv(nil); got != DefaultLimits() {
		t.Fatalf("nil lookup changed limits: %+v", got)
	}
}
