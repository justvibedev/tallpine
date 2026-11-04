//go:build windows

package cleanup

import "io/fs"

// ownedByCurrentUser is false on Windows, where ownership is not read: the
// system temp rule then removes nothing.
func ownedByCurrentUser(fs.FileInfo) bool { return false }
