//go:build !windows

package cleanup

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether info belongs to the daemon's user.
func ownedByCurrentUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
