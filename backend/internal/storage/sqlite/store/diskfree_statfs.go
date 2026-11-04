//go:build darwin || linux

package store

import (
	"fmt"
	"syscall"
)

// diskFree reports the bytes available to this user on the filesystem holding
// path, and an identifier of that filesystem.
func diskFree(path string) (int64, string, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, "", false
	}
	// #nosec G115 -- statfs sizes are far below 2^63; field widths differ by platform
	return int64(st.Bavail) * int64(st.Bsize), fmt.Sprint(st.Fsid), true //nolint:unconvert // field widths differ by platform
}
