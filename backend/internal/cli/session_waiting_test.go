package cli

import "testing"

func TestActivityWithWaiting(t *testing.T) {
	if got := activityWithWaiting("idle", []string{"ci watch"}); got != "idle (waiting on task ci watch)" {
		t.Fatalf("got %q", got)
	}
	if got := activityWithWaiting("idle", nil); got != "idle" {
		t.Fatalf("got %q", got)
	}
	if got := activityWithWaiting("active", []string{"x"}); got != "active" {
		t.Fatalf("got %q", got)
	}
}
