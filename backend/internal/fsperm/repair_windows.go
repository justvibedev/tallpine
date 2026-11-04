//go:build windows

package fsperm

// Windows read-only is a file attribute, not a directory permission, and
// ERROR_ACCESS_DENIED is already handled by the sharing-violation retry loop.
// No Unix-style permission repair applies.
func RepairRemovePermissions(string) bool { return false }
