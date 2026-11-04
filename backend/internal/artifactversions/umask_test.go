//go:build !windows

package artifactversions

import "syscall"

func umaskZero() int     { return syscall.Umask(0) }
func restoreUmask(o int) { syscall.Umask(o) }
