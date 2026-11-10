//go:build darwin

package daemonsock

import (
	"io/fs"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// auditTokenSize is sizeof(audit_token_t).
const auditTokenSize = 32

func fdPeerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return -1, err
	}
	return int(cred.Uid), nil
}

// fdPeerIdentity reads LOCAL_PEERPID and LOCAL_PEERTOKEN. x/sys/unix has no
// wrapper for the token's fixed 32-byte buffer.
func fdPeerIdentity(fd int) (pid int, token [8]uint32, hasToken bool, err error) {
	pid, err = unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	if err != nil {
		return 0, token, false, err
	}
	size := uint32(auditTokenSize)
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, uintptr(fd), unix.SOL_LOCAL, unix.LOCAL_PEERTOKEN, //nolint:staticcheck // SA1019: x/sys/unix exports no getsockopt for this buffer.
		uintptr(unsafe.Pointer(&token)), uintptr(unsafe.Pointer(&size)), 0) // #nosec G103 -- out-parameters of getsockopt(2).
	if errno != 0 {
		return pid, token, false, errno
	}
	return pid, token, size == auditTokenSize, nil
}

func fileUID(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return -1, false
	}
	return int(st.Uid), true
}
