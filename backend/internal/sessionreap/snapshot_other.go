//go:build !darwin && !linux

package sessionreap

import "context"

// Snapshot is unsupported here; the reaper does nothing.
func Snapshot() ([]Proc, error) { return nil, ErrUnsupported }

// procCwd is unsupported here.
func procCwd(context.Context, int) (string, bool) { return "", false }
