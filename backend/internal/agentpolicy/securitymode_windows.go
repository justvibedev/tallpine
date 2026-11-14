//go:build windows

package agentpolicy

// securityModeUnavailable refuses turning Security mode on: its fence (the
// protectState sandbox and the git credential helper) is not built for
// Windows, so "on" would promise protection the agents do not get.
var securityModeUnavailable = "Security mode is not available on Windows"
