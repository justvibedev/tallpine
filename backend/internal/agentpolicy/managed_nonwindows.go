//go:build !windows

package agentpolicy

// programDataDir is only meaningful on Windows.
func programDataDir() string { return "" }
