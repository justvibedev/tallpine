//go:build !windows

package permissioncap

import (
	"os"
	"syscall"
)

// openPointerFile refuses a link at path and does not wait for a FIFO's writer.
func openPointerFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
