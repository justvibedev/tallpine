//go:build darwin

package procmem

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func readParentLink(pid int) (parentLink, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		// An absent pid makes the sysctl return no record, which x/sys/unix
		// reports as EIO.
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EIO) {
			return parentLink{}, ErrProcessGone
		}
		return parentLink{}, fmt.Errorf("procmem: read pid %d: %w", pid, err)
	}
	if int(info.Proc.P_pid) != pid {
		return parentLink{}, ErrProcessGone
	}
	// getsid fails with ESRCH for a zombie too, whose sysctl record survives.
	session, err := unix.Getsid(pid)
	if err != nil {
		if errors.Is(err, unix.ESRCH) {
			return parentLink{}, ErrProcessGone
		}
		return parentLink{}, fmt.Errorf("procmem: session of pid %d: %w", pid, err)
	}
	return parentLink{ppid: int(info.Eproc.Ppid), session: session}, nil
}

// ExecutablePath returns the path pid was executed from, or "" if unreadable.
func ExecutablePath(pid int) string {
	// kern.procargs2 is a 32-bit argc followed by the NUL-terminated exec path.
	const argcSize = 4
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) <= argcSize {
		return ""
	}
	path := buf[argcSize:]
	for i, b := range path {
		if b == 0 {
			return string(path[:i])
		}
	}
	return ""
}

// ChildPIDs returns pid's direct children. It reads the whole process table,
// so it belongs off any request path.
func ChildPIDs(pid int) ([]int, error) {
	infos, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("procmem: children of pid %d: %w", pid, err)
	}
	var children []int
	for i := range infos {
		if int(infos[i].Eproc.Ppid) == pid {
			children = append(children, int(infos[i].Proc.P_pid))
		}
	}
	return children, nil
}
