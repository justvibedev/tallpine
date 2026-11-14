//go:build !windows

package agentpolicy

// securityModeUnavailable is empty where Security mode is supported; see
// securitymode_windows.go.
var securityModeUnavailable = ""
