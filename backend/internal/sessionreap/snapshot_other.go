//go:build !darwin && !linux

package sessionreap

// Snapshot is unsupported here; the reaper does nothing.
func Snapshot() ([]Proc, error) { return nil, ErrUnsupported }
