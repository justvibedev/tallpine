//go:build !windows

package procmem

import (
	"context"
	"fmt"
)

// SnapshotGroups reads the process table with process groups. A nil runner
// uses exec. -A and these column names mean the same on macOS and Linux.
func SnapshotGroups(ctx context.Context, run Runner) (*GroupTable, error) {
	if run == nil {
		run = execRunner
	}
	out, err := run(ctx, "ps", "-A", "-o", "pid=,ppid=,pgid=,stat=,comm=")
	if err != nil {
		return nil, fmt.Errorf("procmem: ps: %w", err)
	}
	return ParseGroups(string(out))
}
