//go:build !windows

package gitworktree

import (
	"errors"
	"io/fs"
)

func isRetryableRemoveError(error) bool { return false }

func isPermissionRemoveError(err error) bool { return errors.Is(err, fs.ErrPermission) }
