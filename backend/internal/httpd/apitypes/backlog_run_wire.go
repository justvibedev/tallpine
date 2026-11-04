package apitypes

import (
	"time"
)

// BacklogRunItemRequest records one planned item's progress.
type BacklogRunItemRequest struct {
	Status   string `json:"status" enum:"queued,active,in_review,needs_you,landed,held,failed"`
	Reason   string `json:"reason,omitempty" description:"Why it is held or failed."`
	Question string `json:"question,omitempty" description:"The one question for the owner (needs_you)."`
}

// BacklogRunItemResponse is one planned item with its progress.
type BacklogRunItemResponse struct {
	Ref           string               `json:"ref"`
	ItemID        string               `json:"itemId"`
	Title         string               `json:"title"`
	Lane          int                  `json:"lane"`
	Position      int                  `json:"position"`
	Model         string               `json:"model"`
	Status        string               `json:"status" description:"What the orchestrator last recorded."`
	DisplayStatus string               `json:"displayStatus" description:"Status to show: landed when the item is done, held when it is held or dropped."`
	Reason        string               `json:"reason,omitempty"`
	PlanStatus    string               `json:"planStatus,omitempty" description:"Status of the item's plan (drafting, awaiting_answers, writing, awaiting_approval, approved, failed); absent without a plan."`
	Question      string               `json:"question,omitempty"`
	Attempts      int                  `json:"attempts"`
	UpdatedAt     time.Time            `json:"updatedAt"`
	Usage         BacklogUsageResponse `json:"usage"`
}

// BacklogRunPauseRequest pauses a run, usually at the Claude usage limit.
type BacklogRunPauseRequest struct {
	Reason   string     `json:"reason,omitempty"`
	ResumeAt *time.Time `json:"resumeAt,omitempty" description:"When the limit resets, for display."`
}

// BacklogRunResponse is a run with its plan.
type BacklogRunResponse struct {
	ID          string                   `json:"id"`
	Ref         string                   `json:"ref"`
	ProjectID   string                   `json:"projectId"`
	Scope       string                   `json:"scope"`
	Selection   string                   `json:"selection"`
	Refs        []string                 `json:"refs"`
	Status      string                   `json:"status" enum:"planning,running,paused,stopping,done"`
	PauseReason string                   `json:"pauseReason,omitempty"`
	ResumeAt    *time.Time               `json:"resumeAt,omitempty"`
	Summary     string                   `json:"summary,omitempty"`
	RequestedBy string                   `json:"requestedBy"`
	CreatedAt   time.Time                `json:"createdAt"`
	UpdatedAt   time.Time                `json:"updatedAt"`
	StartedAt   *time.Time               `json:"startedAt,omitempty"`
	EndedAt     *time.Time               `json:"endedAt,omitempty"`
	Items       []BacklogRunItemResponse `json:"items"`
	Counts      map[string]int           `json:"counts" description:"Planned items per display status."`
	Usage       BacklogUsageResponse     `json:"usage"`
}

// BacklogUsageResponse is what an item's (or run's) linked sessions used.
type BacklogUsageResponse struct {
	CostNanos      *int64 `json:"costNanos,omitempty" description:"Estimated cost in billionths of a USD; absent when nothing is priced."`
	CostComplete   bool   `json:"costComplete" description:"False when some usage events are unpriced, so the cost is a lower bound."`
	ElapsedSeconds *int64 `json:"elapsedSeconds,omitempty" description:"Wall-clock from the first linked session's start to the item's close (or now). Not active time."`
}

// StartBacklogRunRequest asks for a run. An owner request also posts one message to the orchestrator.
type StartBacklogRunRequest struct {
	Scope     string   `json:"scope" enum:"local,pr,pr-merge" description:"How far each item goes: local commit only, a PR, or a merged PR."`
	Selection string   `json:"selection" enum:"all,refs,parent" description:"Which items: every open one, listed refs, or the children of one parent."`
	Refs      []string `json:"refs,omitempty" description:"Item refs for refs; the parent ref for parent."`
}
