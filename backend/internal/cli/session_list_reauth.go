package cli

import "strings"

// activityWithReauth names a session that waits on a harness sign-in, so a
// refused login does not read as an ordinary waiting_input (B520). A running
// turn is the attempt that decides whether the login is back: telling the
// sender to send again while it runs would deliver the message twice.
func activityWithReauth(activity string, reauthRequired bool) string {
	if !reauthRequired || strings.HasPrefix(activity, "active") {
		return activity
	}
	return activity + " (sign-in required: run the harness login, then send again)"
}
