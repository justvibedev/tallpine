//go:build windows

package artifactversions

func umaskZero() int   { return 0 }
func restoreUmask(int) {}
