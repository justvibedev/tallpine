//go:build windows

package agentpolicy

import "golang.org/x/sys/windows"

// programDataDir resolves the machine-wide ProgramData folder through the
// shell known-folder API, which a user cannot redirect with an environment
// variable.
func programDataDir() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return dir
}
