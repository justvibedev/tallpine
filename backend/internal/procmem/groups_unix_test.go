//go:build !windows

package procmem

import (
	"context"
	"slices"
	"testing"
)

func TestSnapshotGroupsAsksPsForGroups(t *testing.T) {
	var gotArgs []string
	table, err := SnapshotGroups(context.Background(), func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotArgs = append([]string{name}, args...)
		return []byte(claudeHostTable), nil
	})
	if err != nil || !table.Alive(102) {
		t.Fatalf("table=%v err=%v", table, err)
	}
	if want := []string{"ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,comm="}; !slices.Equal(gotArgs, want) {
		t.Fatalf("args = %v", gotArgs)
	}
	if _, err := ParseGroups("x y z"); err == nil {
		t.Fatal("malformed row parsed")
	}
}
