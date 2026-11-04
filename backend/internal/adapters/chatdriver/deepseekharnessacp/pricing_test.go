package deepseekharnessacp

import (
	"math"
	"testing"
	"time"
)

func utc(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, time.UTC) }

// 2026-10-26 is a Monday, 2026-10-24 a Saturday, 2026-10-25 a Sunday.
func TestIsPeakBoundariesAndWeekend(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"00:59 before first window", utc(26, 0, 59), false},
		{"01:00 opens first window", utc(26, 1, 0), true},
		{"03:59 last minute of first window", utc(26, 3, 59), true},
		{"04:00 closes first window", utc(26, 4, 0), false},
		{"05:59 gap", utc(26, 5, 59), false},
		{"06:00 opens second window", utc(26, 6, 0), true},
		{"09:59 last minute of second window", utc(26, 9, 59), true},
		{"10:00 closes second window", utc(26, 10, 0), false},
		{"Friday 02:00", utc(30, 2, 0), true},
		{"Saturday 02:00", utc(24, 2, 0), false},
		{"Sunday 07:00", utc(25, 7, 0), false},
	} {
		if got := isPeak(tc.at); got != tc.want {
			t.Errorf("%s: isPeak = %v, want %v", tc.name, got, tc.want)
		}
	}
	// The window is UTC regardless of the timestamp's zone.
	if !isPeak(time.Date(2026, 10, 26, 4, 30, 0, 0, time.FixedZone("x", 3*3600))) {
		t.Error("01:30 UTC given in +03:00 should be peak")
	}
}

func TestRequestCostPeakVersusOffPeak(t *testing.T) {
	// 1M miss + 1M hit + 1M output.
	off, ok := requestCost("deepseek-flash", utc(26, 5, 0), 1e6, 1e6, 1e6)
	if !ok || math.Abs(off-(0.15+0.003+0.60)) > 1e-9 {
		t.Fatalf("off-peak = %v ok=%v", off, ok)
	}
	peak, ok := requestCost("deepseek-flash", utc(26, 2, 0), 1e6, 1e6, 1e6)
	if !ok || math.Abs(peak-(0.30+0.006+1.20)) > 1e-9 {
		t.Fatalf("peak = %v ok=%v", peak, ok)
	}
	weekend, _ := requestCost("deepseek-flash", utc(24, 2, 0), 1e6, 1e6, 1e6)
	if math.Abs(weekend-off) > 1e-9 {
		t.Fatalf("weekend should price off-peak, got %v", weekend)
	}
}

func TestRequestCostUnknownModelHasNoCost(t *testing.T) {
	if c, ok := requestCost("deepseek-v4-pro", utc(26, 5, 0), 100, 100, 100); ok || c != 0 {
		t.Fatalf("unknown model priced: %v %v", c, ok)
	}
}
