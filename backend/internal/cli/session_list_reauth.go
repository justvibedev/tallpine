package cli

// activityWithReauth names a session that waits on a harness sign-in, so a
// refused login does not read as an ordinary waiting_input (B520).
func activityWithReauth(activity string, reauthRequired bool) string {
	if !reauthRequired {
		return activity
	}
	return activity + " (sign-in required: run the harness login, then send again)"
}
