package domain

import "time"

// ContextHealthKind says what a finding is about.
type ContextHealthKind string

const (
	// ContextInstructionBudget marks the always-loaded instruction files being over budget.
	ContextInstructionBudget ContextHealthKind = "instruction_budget"
	// ContextMemoryBudget marks the Claude orchestrator memory index being over budget.
	ContextMemoryBudget ContextHealthKind = "memory_budget"
	// ContextRoutingPointer marks a doc or search that several sessions repeat
	// because the instruction files do not point at it (B826).
	ContextRoutingPointer ContextHealthKind = "routing_pointer"
)

// ContextHealthState is where a finding stands.
type ContextHealthState string

const (
	// ContextFindingQueued waits for a free card slot.
	ContextFindingQueued ContextHealthState = "queued"
	// ContextFindingOpen has an open owner card.
	ContextFindingOpen ContextHealthState = "open"
	// ContextFindingAccepted was answered Yes: a backlog item and worker exist.
	ContextFindingAccepted ContextHealthState = "accepted"
	// ContextFindingDeclined was answered No and is suppressed.
	ContextFindingDeclined ContextHealthState = "declined"
	// ContextFindingResolved is back under its limit.
	ContextFindingResolved ContextHealthState = "resolved"
)

// AskOriginOrchestrator and AskOriginContextHealth say who filed an owner ask.
// An ask with the second origin has no session; the daemon answers for itself.
const (
	AskOriginOrchestrator  = "orchestrator"
	AskOriginContextHealth = "context_health"
)

// Context health suppression after a No: the finding stays quiet until it has
// grown by ContextHealthRegrowPercent of its budget or ContextHealthSuppressFor
// has passed.
const (
	ContextHealthRegrowPercent = 10
	ContextHealthSuppressFor   = 30 * 24 * time.Hour
	// MaxOpenContextCards caps the open finding cards of one project.
	MaxOpenContextCards = 3
	// ContextRoutingMinSessions is how many distinct sessions of a project must
	// read the same doc or run the same search inside ContextRoutingWindow
	// before the finding exists.
	ContextRoutingMinSessions = 3
	ContextRoutingWindow      = 7 * 24 * time.Hour
)

// ContextHealthFinding is one persisted, deduplicated observation.
type ContextHealthFinding struct {
	ID          string
	ProjectID   ProjectID
	Kind        ContextHealthKind
	Fingerprint string
	State       ContextHealthState
	// Evidence is the markdown shown to the owner and handed to the worker.
	Evidence       string
	MeasuredTokens int
	BudgetTokens   int
	// Patch is a unified diff the finder drafted. It is written to the artifact
	// directory when the card is filed and is not persisted.
	Patch         string
	ArtifactDir   string
	AskID         string
	AskRef        string
	BacklogItemID string
	DeclineReason string
	DeclinedAt    time.Time
	// DeclinedTokens is the measurement when the owner said No.
	DeclinedTokens int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Suppressed reports whether a declined finding stays quiet at now with the
// current measurement.
func (f ContextHealthFinding) Suppressed(now time.Time, measured, budget int) bool {
	return f.State == ContextFindingDeclined && f.SuppressionHolds(now, measured, budget)
}

// SuppressionHolds reports whether the owner's No still silences the finding,
// whatever its state: a finding that dipped under budget and grew back keeps
// the decline it had.
func (f ContextHealthFinding) SuppressionHolds(now time.Time, measured, budget int) bool {
	if f.DeclinedAt.IsZero() {
		return false
	}
	if !now.Before(f.DeclinedAt.Add(ContextHealthSuppressFor)) {
		return false
	}
	return measured < f.DeclinedTokens+max(budget*ContextHealthRegrowPercent/100, 1)
}
