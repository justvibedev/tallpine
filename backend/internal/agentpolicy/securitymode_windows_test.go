//go:build windows

package agentpolicy_test

import "testing"

// The real Windows build refuses Security mode without any test override.
func TestSecurityMode_WindowsRefusesOn(t *testing.T) {
	assertUnavailable(t, newHarness(t), "Security mode is not available on Windows")
}
