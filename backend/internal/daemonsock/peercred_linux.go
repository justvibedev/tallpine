//go:build linux

package daemonsock

import (
	"io/fs"
	"syscall"

	"golang.org/x/sys/unix"
)

func fdPeerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}

// fdPeerIdentity reads the peer pid; Linux has no audit token.
func fdPeerIdentity(fd int) (pid int, token [8]uint32, hasToken bool, err error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, token, false, err
	}
	return int(cred.Pid), token, false, nil
}

func fileUID(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, false
	}
	return int(st.Uid), true
}
