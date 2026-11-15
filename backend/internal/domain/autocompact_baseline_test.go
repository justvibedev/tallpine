package domain

import "testing"

func TestResolveAutoCompactWindowFloorsAtBaseline(t *testing.T) {
	const opus, haiku = 1_000_000, 200_000
	for _, tt := range []struct {
		name     string
		value    string
		window   int64
		baseline int64
		want     int
	}{
		{"explicit 120k with a 66k baseline is 166k", "120k", opus, 66_000, 166_000},
		{"orchestrator 120k with a 51k baseline is 151k", "120k", opus, 51_000, 151_000},
		{"the floor never lowers a larger choice", "40%", opus, 66_000, 400_000},
		{"the default baseline floors at 170k", "120k", opus, AutoCompactBaselineDefault, 170_000},
		{"a floor at the model window passes nothing", "120k", haiku, 100_000, 0},
		{"a floor above the model window passes nothing", "120k", haiku, 150_000, 0},
		{"a floor past 1M on a 1M model passes nothing", "120k", opus, 950_000, 0},
		{"auto stays the agent default", "", opus, 66_000, 0},
		{"a percent with an unknown window stays unresolved", "40%", 0, 66_000, 0},
		{"an explicit size with an unknown window is floored", "120k", 0, 66_000, 166_000},
		{"no baseline leaves the choice", "120k", opus, 0, 120_000},
	} {
		if got := ResolveAutoCompactWindow(tt.value, tt.window, tt.baseline); got != tt.want {
			t.Errorf("%s: ResolveAutoCompactWindow(%q, %d, %d) = %d, want %d", tt.name, tt.value, tt.window, tt.baseline, got, tt.want)
		}
	}
}

func TestAutoCompactBaselineMedian(t *testing.T) {
	for _, tt := range []struct {
		name    string
		samples []int64
		want    int64
	}{
		{"no samples use the default", nil, AutoCompactBaselineDefault},
		{"two samples use the default", []int64{40_000, 50_000}, 70_000},
		{"three samples take the middle", []int64{90_000, 30_000, 51_000}, 51_000},
		{"an even count takes the mean of the middle two", []int64{60_000, 10_000, 72_000, 66_000}, 63_000},
	} {
		if got := AutoCompactBaseline(tt.samples); got != tt.want {
			t.Errorf("%s: AutoCompactBaseline(%v) = %d, want %d", tt.name, tt.samples, got, tt.want)
		}
	}
}

func TestAutoCompactBaselineKind(t *testing.T) {
	if got := AutoCompactBaselineKind(KindOrchestrator); got != KindOrchestrator {
		t.Errorf("orchestrator measured as %q", got)
	}
	for _, kind := range []SessionKind{KindWorker, "planner", "reviewer", ""} {
		if got := AutoCompactBaselineKind(kind); got != KindWorker {
			t.Errorf("%q measured as %q, want worker", kind, got)
		}
	}
}

func TestAutoCompactTrigger(t *testing.T) {
	if got := AutoCompactTrigger(HarnessClaudeCode, 166_000); got != 133_000 {
		t.Errorf("Claude Code trigger = %d, want 133000", got)
	}
	if got := AutoCompactTrigger(HarnessCodex, 166_000); got != 166_000 {
		t.Errorf("Codex trigger = %d, want 166000", got)
	}
	if got := AutoCompactTrigger(HarnessClaudeCode, 0); got != 0 {
		t.Errorf("agent default trigger = %d, want 0", got)
	}
}

func TestAutoCompactRoomTight(t *testing.T) {
	// Haiku 200k with a 100k baseline: 200 - 33 - 100 - 8 = 59k of room.
	if !AutoCompactRoomTight(200_000, 100_000) {
		t.Error("59k of room should warn")
	}
	// 200 - 33 - 99 - 8 = 60k: just enough.
	if AutoCompactRoomTight(200_000, 99_000) {
		t.Error("60k of room should not warn")
	}
	if AutoCompactRoomTight(0, 500_000) {
		t.Error("an unknown model window never warns")
	}
	// A floor at or above the model window both passes nothing and warns.
	if ResolveAutoCompactWindow("120k", 200_000, 120_000) != 0 || !AutoCompactRoomTight(200_000, 120_000) {
		t.Error("a floor past the model window should pass nothing and warn")
	}
}

func TestIdleCompactThresholdFloorsAtBaseline(t *testing.T) {
	// Default 30% of a 151k window is 45.3k; a 51k baseline raises it to 81k.
	if got := IdleCompactThreshold("", 151_000, 51_000); got != 81_000 {
		t.Errorf("threshold = %d, want 81000", got)
	}
	// A larger setting is kept.
	if got := IdleCompactThreshold("100k", 151_000, 51_000); got != 100_000 {
		t.Errorf("threshold = %d, want 100000", got)
	}
	// Off stays off.
	if got := IdleCompactThreshold(AutoCompactOff, 151_000, 51_000); got != 0 {
		t.Errorf("off threshold = %d, want 0", got)
	}
	// A floor at or past the window: the provider compacts first anyway.
	if got := IdleCompactThreshold("", 151_000, 130_000); got != 0 {
		t.Errorf("threshold past the window = %d, want 0", got)
	}
}
