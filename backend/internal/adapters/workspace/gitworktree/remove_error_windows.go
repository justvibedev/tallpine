//go:build windows

package gitworktree

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isRetryableRemoveError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

// Windows read-only is a file attribute, not a directory permission, and
// ERROR_ACCESS_DENIED is already handled by the sharing-violation retry loop.
// No Unix-style permission repair applies.
func isPermissionRemoveError(error) bool { return false }
