//go:build windows

package cleanup

import (
	"context"
	"errors"
)

// pathInUse cannot tell on Windows, so the caller keeps the path. The system
// temp rule removes nothing there anyway (see ownedByCurrentUser).
func pathInUse(context.Context, string) (bool, error) {
	return false, errors.New("in-use check is not supported on Windows")
}
