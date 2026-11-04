//go:build !windows

package storageusage

import (
	"io/fs"
	"syscall"
)

type inodeKey struct{ dev, ino uint64 }

// diskBytes is the space the file occupies (allocated blocks, as du reports
// it) and its inode when hard-linked, so a linked file counts once.
func diskBytes(info fs.FileInfo) (int64, inodeKey, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), inodeKey{}, false
	}
	//nolint:gosec,unconvert // Stat_t field widths differ per platform.
	return int64(st.Blocks) * 512, inodeKey{uint64(st.Dev), uint64(st.Ino)}, st.Nlink > 1
}
