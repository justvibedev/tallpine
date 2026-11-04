package domain

import "time"

// AccountFailoverRetry is a usage-limited turn whose session was moved to
// another account and whose retry on the new account is still owed. It is
// stored so the retry survives a daemon restart: the move's commit disarms the
// turn's auto-resume, so nothing else would bring the turn back.
type AccountFailoverRetry struct {
	TurnID    string
	SessionID SessionID
	MoveID    string
	// Moved is set once the move ended on the new account.
	Moved    bool
	Attempts int
	// RetryAt is when the next attempt may run; zero means now.
	RetryAt   time.Time
	CreatedAt time.Time
}
