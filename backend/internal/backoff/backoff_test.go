package backoff

import (
	"testing"
	"time"
)

func TestTable(t *testing.T) {
	d := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	for failures, want := range map[int]time.Duration{-1: 30 * time.Second, 0: 30 * time.Second, 1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute, 4: 5 * time.Minute, 99: 5 * time.Minute} {
		if got := Table(d, failures); got != want {
			t.Errorf("Table(%d)=%s want %s", failures, got, want)
		}
	}
	if Table(nil, 3) != 0 {
		t.Error("empty table must yield 0")
	}
}

func TestDoublingCapsAndNeverOverflows(t *testing.T) {
	for failures, want := range map[int]time.Duration{0: 5 * time.Second, 1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 5: 80 * time.Second, 6: 2 * time.Minute, 1 << 30: 2 * time.Minute} {
		if got := Doubling(5*time.Second, 2*time.Minute, failures); got != want {
			t.Errorf("Doubling(%d)=%s want %s", failures, got, want)
		}
	}
	if got := Doubling(time.Hour, time.Minute, 1); got != time.Minute {
		t.Errorf("initial above cap must be capped, got %s", got)
	}
}

func TestDouble(t *testing.T) {
	if got := Double(40*time.Minute, time.Hour); got != time.Hour {
		t.Errorf("Double capped=%s", got)
	}
	if got := Double(time.Minute, time.Hour); got != 2*time.Minute {
		t.Errorf("Double=%s", got)
	}
}

// The schedules below are the exact delays each retry loop used before they
// shared this package; they are pinned so a refactor cannot shift them.
func TestChatWakeScheduleIsUnchanged(t *testing.T) {
	const base = 30 * time.Second
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute}
	for i, w := range want {
		if got := Doubling(base, base<<5, i+1); got != w {
			t.Errorf("chat wake failure %d = %s, want %s (old base << (n-1))", i+1, got, w)
		}
	}
}

func TestReportDeliveryScheduleIsUnchanged(t *testing.T) {
	want := []time.Duration{5 * time.Second, 10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute, 2 * time.Minute}
	for i, w := range want {
		if got := Doubling(5*time.Second, 2*time.Minute, i+1); got != w {
			t.Errorf("report failure %d = %s, want %s", i+1, got, w)
		}
	}
}

func TestFixedTablesAreUnchanged(t *testing.T) {
	source := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	readiness := []time.Duration{15 * time.Second, time.Minute, 5 * time.Minute}
	catalog := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}
	for _, c := range []struct {
		name  string
		table []time.Duration
	}{{"source", source}, {"readiness", readiness}, {"catalog", catalog}} {
		for i, w := range c.table {
			if got := Table(c.table, i+1); got != w {
				t.Errorf("%s failure %d = %s, want %s", c.name, i+1, got, w)
			}
		}
		if got := Table(c.table, 50); got != c.table[len(c.table)-1] {
			t.Errorf("%s past the end = %s", c.name, got)
		}
	}
}

func TestCostBackfillScheduleIsUnchanged(t *testing.T) {
	d, got := time.Minute, []time.Duration{}
	for i := 0; i < 8; i++ {
		got = append(got, d)
		d = Double(d, time.Hour)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i, w := range want {
		if got[i] != w*time.Minute {
			t.Errorf("backfill retry %d = %s, want %dm", i+1, got[i], w)
		}
	}
}
