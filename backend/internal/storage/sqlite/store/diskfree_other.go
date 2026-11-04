//go:build !darwin && !linux

package store

// diskFree is unknown here, so a disk-hungry conversion never starts.
func diskFree(string) (int64, string, bool) { return 0, "", false }
