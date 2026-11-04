//go:build !windows

package deepseekharness

import (
	"context"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A dsh child (node) that outlives the test is the B151 leak. The helper must
// reap grandchildren, including after the direct child exits cleanly.
func TestCombinedOutputGroupedLeavesNoGrandchild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, _, err := combinedOutputGrouped(ctx, "sh", []string{"-c", "sleep 300 & echo $!"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	gc, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("grandchild pid from %q: %v", out, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(gc, 0) != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(gc, syscall.SIGKILL)
	t.Fatalf("grandchild %d outlived the helper", gc)
}
