//go:build !darwin

package gitworktree

// cloneSupported reports whether this platform has a directory clone primitive.
const cloneSupported = false

func cloneDir(src, dst string) error {
	return errCloneUnsupported
}
