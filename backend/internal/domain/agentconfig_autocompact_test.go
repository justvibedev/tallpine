package domain

import "testing"

func TestResolveAutoCompactWindow(t *testing.T) {
	const opus, haiku, codex = 1_000_000, 200_000, 272_000
	for _, tt := range []struct {
		name   string
		value  string
		window int64
		want   int
	}{
		// The B734 table: a percent is of a fixed 1M, never of the model window.
		{"Opus 1M at 40% is 400k", "40%", opus, 400_000},
		{"Haiku 200k at 40% passes nothing", "40%", haiku, 0},
		{"Codex window at 60% passes nothing", "60%", codex, 0},
		{"explicit 120k is unchanged", "120k", opus, 120_000},
		{"explicit 120k with an unknown window is unchanged", "120k", 0, 120_000},
		{"unknown window passes nothing", "40%", 0, 0},
		{"a percent at the model window passes nothing", "20%", haiku, 0},
		{"an explicit size at the model window passes nothing", "200k", haiku, 0},
		{"an explicit size above the model window passes nothing", "250k", haiku, 0},
		{"1m on a 1M model passes nothing", "1m", opus, 0},
		{"a percent is the same on every larger window", "40%", 2_000_000, 400_000},
		{"Codex window at 20% is 200k", "20%", codex, 200_000},
		{"just below the model window is kept", "199k", haiku, 199_000},
		{"1m preset with an unknown window", "1m", 0, 1_000_000},
		{"highest percent", "95%", opus, 950_000},
		{"auto leaves the default", "", opus, 0},
		{"percent below the range", "19%", opus, 0},
		{"percent above the range", "96%", opus, 0},
		{"unrecognised value", "70", opus, 0},
	} {
		if got := ResolveAutoCompactWindow(tt.value, tt.window, 0); got != tt.want {
			t.Errorf("%s: ResolveAutoCompactWindow(%q, %d) = %d, want %d", tt.name, tt.value, tt.window, got, tt.want)
		}
	}
}

func TestValidAutoCompactWindow(t *testing.T) {
	for _, ok := range []string{"", "200k", "120k", "50k", "1m", "20%", "40%", "50%", "75%", "90%", "95%"} {
		if !ValidAutoCompactWindow(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"19%", "96%", "100%", "0%", "4%", "040%", "-40%", "+40%", "4 0%", "40.5%", "%", "49k", "1001k", "0k", "k", "1m0", "70", "40%%", " 40%"} {
		if ValidAutoCompactWindow(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestResolveAutoCompactWindowClampsExplicitSizes(t *testing.T) {
	for value, want := range map[string]int{"50k": 100_000, "99k": 100_000, "120k": 120_000, "1m": 1_000_000} {
		if got := ResolveAutoCompactWindow(value, 0, 0); got != want {
			t.Errorf("ResolveAutoCompactWindow(%q) = %d, want %d", value, got, want)
		}
	}
}
