//go:build !windows

package unixsock

import "syscall"

func setUmask(m int) int { return syscall.Umask(m) }
