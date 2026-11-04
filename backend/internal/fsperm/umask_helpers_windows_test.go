//go:build windows

package fsperm

func syscallUmask() int { return 0 }
func restoreUmask(int)  {}
func setUmask(int)      {}
