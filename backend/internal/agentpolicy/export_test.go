package agentpolicy

// SetSecurityModeUnavailableForTest swaps the platform's Security mode refusal
// so the guard is testable on every OS; the returned func restores it.
func SetSecurityModeUnavailableForTest(msg string) (restore func()) {
	prev := securityModeUnavailable
	securityModeUnavailable = msg
	return func() { securityModeUnavailable = prev }
}
