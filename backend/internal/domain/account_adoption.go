package domain

import "time"

// AccountAdoptionPolicy is how a bulk account switch treats a turn in progress:
// wait for it to finish, or stop it.
type AccountAdoptionPolicy string

const (
	// AccountAdoptionWait lets a busy session finish its turn first.
	AccountAdoptionWait AccountAdoptionPolicy = "wait"
	// AccountAdoptionInterrupt stops the turn in progress.
	AccountAdoptionInterrupt AccountAdoptionPolicy = "interrupt"
)

// Valid reports a known policy.
func (p AccountAdoptionPolicy) Valid() bool {
	return p == AccountAdoptionWait || p == AccountAdoptionInterrupt
}

// AccountAdoptionState is a batch's progress.
type AccountAdoptionState string

const (
	// AccountAdoptionRunning has sessions still to settle.
	AccountAdoptionRunning AccountAdoptionState = "running"
	// AccountAdoptionCompleted has an outcome for every session.
	AccountAdoptionCompleted AccountAdoptionState = "completed"
)

// AccountAdoptionItemState is one session's outcome in a batch.
type AccountAdoptionItemState string

const (
	// AccountItemQueued has not started.
	AccountItemQueued AccountAdoptionItemState = "queued"
	// AccountItemMoving is in its handoff (preflight, stop, start).
	AccountItemMoving AccountAdoptionItemState = "moving"
	// AccountItemWaiting is busy: its handoff waits for the turn to finish.
	AccountItemWaiting AccountAdoptionItemState = "waiting"
	// AccountItemMoved moved to the account.
	AccountItemMoved AccountAdoptionItemState = "moved"
	// AccountItemRebound had no running agent; only its binding changed.
	AccountItemRebound AccountAdoptionItemState = "rebound"
	// AccountItemFailed stayed on its old account; ErrorCode says why.
	AccountItemFailed AccountAdoptionItemState = "failed"
	// AccountItemSkipped needed no move by the time its turn came.
	AccountItemSkipped AccountAdoptionItemState = "skipped"
)

// Terminal reports a settled outcome.
func (s AccountAdoptionItemState) Terminal() bool {
	switch s {
	case AccountItemMoved, AccountItemRebound, AccountItemFailed, AccountItemSkipped:
		return true
	}
	return false
}

// AccountAdoptionItem is one session of a batch.
type AccountAdoptionItem struct {
	SessionID      SessionID                `json:"sessionId"`
	Position       int                      `json:"position"`
	IsOrchestrator bool                     `json:"isOrchestrator"`
	State          AccountAdoptionItemState `json:"state"`
	TransitionID   string                   `json:"-"`
	ErrorCode      string                   `json:"errorCode,omitempty"`
	ErrorMessage   string                   `json:"errorMessage,omitempty"`
	UpdatedAt      time.Time                `json:"updatedAt"`
}

// AccountAdoption is a batch that switches the live sessions of AccountID's
// provider to that account.
type AccountAdoption struct {
	ID         string                `json:"id"`
	AccountID  string                `json:"accountId"`
	Provider   AgentAccountProvider  `json:"provider"`
	Policy     AccountAdoptionPolicy `json:"policy"`
	RequestKey string                `json:"-"`
	State      AccountAdoptionState  `json:"state"`
	CreatedAt  time.Time             `json:"createdAt"`
	UpdatedAt  time.Time             `json:"updatedAt"`
	Items      []AccountAdoptionItem `json:"items"`
}
