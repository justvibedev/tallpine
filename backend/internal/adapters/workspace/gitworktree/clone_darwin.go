//go:build darwin

package gitworktree

import "golang.org/x/sys/unix"

// cloneSupported reports whether this platform has a directory clone primitive.
const cloneSupported = true

// cloneDir clones the directory tree at src to the non-existent dst with a
// single clonefile(2) call. It fails with EXDEV/ENOTSUP when the two paths are
// not on the same APFS volume. CLONE_NOFOLLOW keeps a src that was swapped for
// a symlink after validation from being followed.
func cloneDir(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}
