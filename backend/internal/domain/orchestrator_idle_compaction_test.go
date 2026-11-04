package domain

import (
	"testing"
	"time"
)

func TestIdleCompactThreshold(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		setting string
		window  int64
		want    int64
	}{
		{"", 200_000, 60_000},
		{"", 120_000, 36_000},
		{"40%", 200_000, 80_000},
		{"200k", 1_000_000, 200_000},
		{"60k", 120_000, 60_000},
		{"200k", 200_000, 0}, // not below the window: the provider compacts anyway
		{"50%", 0, 0},        // unknown window
		{AutoCompactOff, 200_000, 0},
	} {
		if got := IdleCompactThreshold(tc.setting, tc.window); got != tc.want {
			t.Errorf("IdleCompactThreshold(%q, %d) = %d, want %d", tc.setting, tc.window, got, tc.want)
		}
	}
}

func TestIdleCompactSkipReason(t *testing.T) {
	t.Parallel()
	ready := IdleCompactFacts{IdleFor: IdleCompactWait, ContextUsed: 150_000, Threshold: 100_000}
	if r := ready.IdleCompactSkipReason(); r != "" {
		t.Fatalf("qualifying orchestrator skipped: %s", r)
	}
	for name, mutate := range map[string]func(*IdleCompactFacts){
		"off":                      func(f *IdleCompactFacts) { f.Threshold = 0 },
		"busy":                     func(f *IdleCompactFacts) { f.Busy = true },
		"recently active":          func(f *IdleCompactFacts) { f.IdleFor = IdleCompactWait - time.Second },
		"below threshold":          func(f *IdleCompactFacts) { f.ContextUsed = 99_999 },
		"already compacted":        func(f *IdleCompactFacts) { f.CompactedAtThisActivity = true },
		"background tasks running": func(f *IdleCompactFacts) { f.BackgroundTasks = 1 },
		"reports pending":          func(f *IdleCompactFacts) { f.PendingReports = 1 },
		"workers in flight":        func(f *IdleCompactFacts) { f.InFlightWorkers = []SessionID{"w"} },
		"reviews running":          func(f *IdleCompactFacts) { f.RunningReviews = 1 },
	} {
		f := ready
		mutate(&f)
		if got := f.IdleCompactSkipReason(); got != name {
			t.Errorf("%s: reason = %q", name, got)
		}
	}
}

func TestProjectConfigValidatesIdleCompactAt(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"", "50%", "200k", AutoCompactOff} {
		if err := (ProjectConfig{Orchestrator: RoleOverride{IdleCompactAt: ok}}).Validate(); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"10%", "100000", "on"} {
		if err := (ProjectConfig{Orchestrator: RoleOverride{IdleCompactAt: bad}}).Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if err := (ProjectConfig{Worker: RoleOverride{IdleCompactAt: "50%"}}).Validate(); err == nil {
		t.Error("worker.idleCompactAt accepted")
	}
}

func TestIdleActionValidation(t *testing.T) {
	for _, ok := range []string{"", IdleActionCompact, IdleActionHandoff} {
		cfg := ProjectConfig{Orchestrator: RoleOverride{IdleAction: ok}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("idleAction %q: %v", ok, err)
		}
	}
	for name, cfg := range map[string]ProjectConfig{
		"unknown": {Orchestrator: RoleOverride{IdleAction: "delete"}},
		"worker":  {Worker: RoleOverride{IdleAction: IdleActionHandoff}},
		"planner": {Planner: RoleOverride{IdleAction: IdleActionHandoff}},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
