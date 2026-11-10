package domain

import (
	"errors"
	"strconv"
	"time"
)

// BacklogRunStatus is where a run stands: planning until the orchestrator
// writes the plan, then running, paused (usage limit) or stopping (finish what
// is in flight), and done.
type BacklogRunStatus string

// Run statuses.
const (
	BacklogRunPlanning BacklogRunStatus = "planning"
	BacklogRunRunning  BacklogRunStatus = "running"
	BacklogRunPaused   BacklogRunStatus = "paused"
	BacklogRunStopping BacklogRunStatus = "stopping"
	BacklogRunDone     BacklogRunStatus = "done"
)

// Open reports whether the run still occupies the project's single run slot.
func (s BacklogRunStatus) Open() bool { return s != BacklogRunDone && s != "" }

// BacklogRunItemStatus is one planned item's progress within a run.
type BacklogRunItemStatus string

// Run item statuses.
const (
	BacklogRunItemQueued   BacklogRunItemStatus = "queued"
	BacklogRunItemActive   BacklogRunItemStatus = "active"
	BacklogRunItemInReview BacklogRunItemStatus = "in_review"
	BacklogRunItemNeedsYou BacklogRunItemStatus = "needs_you"
	BacklogRunItemLanded   BacklogRunItemStatus = "landed"
	BacklogRunItemHeld     BacklogRunItemStatus = "held"
	BacklogRunItemFailed   BacklogRunItemStatus = "failed"
)

// Valid reports whether s is a known item status.
func (s BacklogRunItemStatus) Valid() bool {
	switch s {
	case BacklogRunItemQueued, BacklogRunItemActive, BacklogRunItemInReview, BacklogRunItemNeedsYou,
		BacklogRunItemLanded, BacklogRunItemHeld, BacklogRunItemFailed:
		return true
	}
	return false
}

// InFlight reports whether the item still has work running (a worker or a review).
func (s BacklogRunItemStatus) InFlight() bool {
	return s == BacklogRunItemActive || s == BacklogRunItemInReview
}

// Settled reports whether nothing more will happen to the item in this run
// without someone's decision (needs_you counts: the run carries on without it).
// A first failure is not settled: the orchestrator retries it or it is held.
func (s BacklogRunItemStatus) Settled() bool {
	switch s {
	case BacklogRunItemLanded, BacklogRunItemHeld, BacklogRunItemNeedsYou:
		return true
	}
	return false
}

// Run limits enforced in code.
const (
	// MaxBacklogRunWorkers is how many items may be active at once per project.
	MaxBacklogRunWorkers = 3
	// MaxBacklogRunAttempts is how many failures hold an item.
	MaxBacklogRunAttempts = 2
	MaxBacklogRunSummary  = 20000
	MaxBacklogRunReason   = 500
	MaxBacklogRunQuestion = 1000
	MaxBacklogRunModel    = 100
)

// BacklogRunSelection says which items a run was asked to cover.
type BacklogRunSelection string

// Run selection kinds.
const (
	BacklogRunSelectAll    BacklogRunSelection = "all"
	BacklogRunSelectRefs   BacklogRunSelection = "refs"
	BacklogRunSelectParent BacklogRunSelection = "parent"
)

// Valid reports whether s is a known selection kind.
func (s BacklogRunSelection) Valid() bool {
	return s == BacklogRunSelectAll || s == BacklogRunSelectRefs || s == BacklogRunSelectParent
}

// BacklogRun is one run-mode request and its lifecycle.
type BacklogRun struct {
	ID            string
	ProjectID     ProjectID
	Seq           int64
	Scope         BacklogScope
	SelectionKind BacklogRunSelection
	// Selection holds item refs for refs, the parent ref for parent, none for all.
	Selection   []string
	Status      BacklogRunStatus
	PauseReason string
	ResumeAt    *time.Time
	// Summary is the orchestrator's end-of-run narrative.
	Summary     string
	RequestedBy BacklogOriginKind
	CreatedAt   time.Time
	UpdatedAt   time.Time
	StartedAt   *time.Time
	EndedAt     *time.Time
}

// Ref is the run's display ref, "R3".
func (r BacklogRun) Ref() string { return "R" + strconv.FormatInt(r.Seq, 10) }

// BacklogRunItem is one planned item: its lane, model and progress.
type BacklogRunItem struct {
	RunID    string
	ItemID   string
	Lane     int
	Position int
	Model    string
	Status   BacklogRunItemStatus
	Reason   string
	Question string
	// Attempts counts failures; the second one holds the item.
	Attempts  int
	UpdatedAt time.Time
	// Joined from the item for display and rules.
	ItemSeq   int64
	ItemTitle string
	ItemState BacklogState
	ItemHold  string
}

// ItemRef is the item's display ref, "B12".
func (i BacklogRunItem) ItemRef() string { return "B" + strconv.FormatInt(i.ItemSeq, 10) }

// Backlog run errors.
var (
	ErrBacklogRunNotFound   = errors.New("backlog run not found")
	ErrBacklogRunOpen       = errors.New("backlog run already open")
	ErrBacklogRunInvalid    = errors.New("invalid backlog run")
	ErrBacklogRunTransition = errors.New("backlog run transition not allowed")
	ErrBacklogRunWorkerCap  = errors.New("backlog run worker cap reached")
	ErrBacklogRunDisabled   = errors.New("backlog run mode is off")
)

// BacklogRunReasonPlanPending is the reason a run item carries while it waits
// for its own plan (B205): the planner has not started, or the owner has not
// approved the plan. The run does not start the item until then.
const BacklogRunReasonPlanPending = "plan_pending"

// BacklogRunReasonPlanCancelled marks a run item whose plan the owner cancelled:
// it waits for the owner to plan it again (or skip it), is not planned
// automatically even in always mode, and does not hold the run open.
const BacklogRunReasonPlanCancelled = "plan_cancelled"
