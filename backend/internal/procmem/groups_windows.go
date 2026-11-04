//go:build windows

package procmem

import "context"

// SnapshotGroups is unsupported on Windows, which has no process groups in
// this sense; a caller treats the error as "cannot tell".
func SnapshotGroups(context.Context, Runner) (*GroupTable, error) {
	return nil, ErrUnsupported
}
