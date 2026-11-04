//go:build !windows

package browserruntime

import (
	"net"
	"path/filepath"

	"tallpine.eu/tallpine/backend/internal/unixsock"
)

// Listen creates the local daemon-to-Electron browser bridge listener: a
// private, owner-UID-only socket reached through a short alias.
func Listen(runFilePath string) (net.Listener, string, error) {
	return listenAt(unixsock.ShortAliasRoot(), runFilePath)
}

func listenAt(aliasRoot, runFilePath string) (net.Listener, string, error) {
	return unixsock.ListenAliasedAt(aliasRoot, filepath.Dir(runFilePath), "tallpine-brd-", "browser.sock")
}
