//go:build windows

package update

// The release-checkout updater drives bash and launchd-era tooling; it is not
// available on Windows.

// pidAlive is never consulted for a real job on Windows: no job can start.
func pidAlive(pid int) bool { return false }

// launchDetached always fails: updates are unavailable on Windows.
func launchDetached(script string, env []string, logPath string) (int, error) {
	return 0, ErrUnavailable
}
