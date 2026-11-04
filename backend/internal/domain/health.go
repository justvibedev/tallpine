package domain

import "time"

// StuckReportBatch is one project's worker reports that were due before a
// deadline yet are still not acknowledged.
type StuckReportBatch struct {
	ProjectID ProjectID
	Pending   int
	Claimed   int
	Since     time.Time
}

// OverdueTurnFallback is a pending final-result notice whose due time passed.
type OverdueTurnFallback struct {
	ID        string
	SessionID SessionID
	DueAt     time.Time
}
