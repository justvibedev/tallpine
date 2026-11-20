package contextbudget

import (
	"strings"
	"testing"
	"time"
)

func TestFirstCheckAfterRestartIgnoresTheDailyGate(t *testing.T) {
	r := newRig(t, true)
	r.sweep(0)
	r.claude = strings.Repeat("a", 30000)
	r.sweep(time.Hour) // a new Sweeper: the daemon restarted with new rules
	if got := r.findings()["instruction_budget"].MeasuredTokens; got == 1100 {
		t.Fatalf("measured = %d, want a fresh check after restart", got)
	}
}
