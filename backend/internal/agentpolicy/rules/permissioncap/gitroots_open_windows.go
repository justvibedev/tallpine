//go:build windows

package permissioncap

import (
	"errors"
	"os"
)

// openPointerFile refuses a link at path. Windows has no O_NOFOLLOW, and no
// FIFO a file open could wait on.
func openPointerFile(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("permissioncap: pointer file is a symlink")
	}
	return os.Open(path)
}
