package domain

import "testing"

func TestAutoCompactTokenCount(t *testing.T) {
	for _, tt := range []struct {
		name   string
		value  string
		window int64
		want   int
	}{
		{"preset ignores the window", "200k", 0, 200_000},
		{"1m preset", "1m", 200_000, 1_000_000},
		{"percent of 200k", "70%", 200_000, 140_000},
		{"percent of 1m", "90%", 1_000_000, 900_000},
		{"percent clamps to the 100k floor", "50%", 150_000, 100_000},
		{"percent clamps to the 1M ceiling", "90%", 2_000_000, 1_000_000},
		{"unknown window is never guessed", "70%", 0, 0},
		{"auto leaves the default", "", 200_000, 0},
		{"any whole percent in range", "75%", 200_000, 150_000},
		{"the 40% default on a 1M window", "40%", 1_000_000, 400_000},
		{"lowest percent", "20%", 1_000_000, 200_000},
		{"percent below the range", "19%", 1_000_000, 0},
		{"percent above the range", "96%", 1_000_000, 0},
		{"unrecognised value", "70", 200_000, 0},
	} {
		if got := AutoCompactTokenCount(tt.value, tt.window); got != tt.want {
			t.Errorf("%s: AutoCompactTokenCount(%q, %d) = %d, want %d", tt.name, tt.value, tt.window, got, tt.want)
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

func TestAutoCompactTokenCountClampsExplicitSizes(t *testing.T) {
	for value, want := range map[string]int{"50k": 100_000, "99k": 100_000, "120k": 120_000, "1m": 1_000_000} {
		if got := AutoCompactTokenCount(value, 0); got != want {
			t.Errorf("AutoCompactTokenCount(%q) = %d, want %d", value, got, want)
		}
	}
}
