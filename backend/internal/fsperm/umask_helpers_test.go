//go:build !windows

package fsperm

import "syscall"

func syscallUmask() int  { o := syscall.Umask(0); syscall.Umask(o); return o }
func restoreUmask(o int) { syscall.Umask(o) }
func setUmask(m int)     { syscall.Umask(m) }
