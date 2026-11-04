package domain

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// PlanStatus is where a backlog plan is in its lifecycle (B205).
type PlanStatus string

// The plan statuses, in lifecycle order.
const (
	PlanDrafting         PlanStatus = "drafting"          // planner explores, nothing submitted yet
	PlanAwaitingAnswers  PlanStatus = "awaiting_answers"  // questions and assumptions wait for the owner
	PlanWriting          PlanStatus = "writing"           // answers sent, planner writes the plan
	PlanAwaitingApproval PlanStatus = "awaiting_approval" // plan submitted, owner approves
	PlanApproved         PlanStatus = "approved"
	PlanSuperseded       PlanStatus = "superseded" // replaced by a re-plan, or the item closed
	PlanFailed           PlanStatus = "failed"     // planner ended without submitting
)

// Open reports whether the plan still needs work: at most one open plan per item.
func (s PlanStatus) Open() bool {
	switch s {
	case PlanApproved, PlanSuperseded, PlanFailed:
		return false
	}
	return s != ""
}

// planTransitions is the state machine. A same-status write is not a transition.
var planTransitions = map[PlanStatus][]PlanStatus{
	PlanDrafting:         {PlanAwaitingAnswers, PlanWriting, PlanFailed, PlanSuperseded},
	PlanAwaitingAnswers:  {PlanWriting, PlanFailed, PlanSuperseded},
	PlanWriting:          {PlanAwaitingApproval, PlanFailed, PlanSuperseded},
	PlanAwaitingApproval: {PlanApproved, PlanSuperseded, PlanFailed},
	PlanApproved:         {PlanSuperseded},
	PlanFailed:           {PlanSuperseded},
}

// CanTransition reports whether a plan may move from one status to another.
func (s PlanStatus) CanTransition(to PlanStatus) bool {
	for _, t := range planTransitions[s] {
		if t == to {
			return true
		}
	}
	return false
}

// Caps on what a planner may submit; the CLI and the service both enforce them.
const (
	MaxPlanQuestions        = 5
	MaxPlanAssumptions      = 20
	MaxPlanOptions          = 6
	MaxPlanQuestionChars    = 500
	MaxPlanAssumptionChars  = 300
	MaxPlanMarkdownBytes    = 60000
	MaxPlanDecisionsBytes   = 8000
	MaxPlanReviewFocus      = 5
	MaxPlanFocusChars       = 300
	MaxPlanAnswerChars      = 2000
	MaxPlanPRs              = 30
	MaxPlanPRFiles          = 200
	MaxPlanOptionLabelChars = 200
)

// PlanOption is one answer choice of a question.
type PlanOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PlanQuestion is a question for the owner with the planner's recommendation.
type PlanQuestion struct {
	ID          string       `json:"id"`
	Text        string       `json:"text"`
	Reason      string       `json:"reason,omitempty"`
	Options     []PlanOption `json:"options"`
	Recommended string       `json:"recommended"`
}

// PlanAssumption is a one-line assumption the owner accepts by default.
type PlanAssumption struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// ScopeCut marks an assumption that drops or postpones scope, so the plan
	// card can highlight it. Absent in plans stored before the field existed.
	ScopeCut bool `json:"scopeCut,omitempty"`
}

// PlanAnswer answers one question: an option id, or free text (Other).
type PlanAnswer struct {
	Option string `json:"option,omitempty"`
	Other  string `json:"other,omitempty"`
}

// PlanAnswers is what the owner (or the orchestrator relaying the owner) said.
type PlanAnswers struct {
	Questions   map[string]PlanAnswer `json:"questions"`
	Corrections map[string]string     `json:"corrections,omitempty"`
	// By is "user" or "orchestrator:<session id>".
	By       string    `json:"by"`
	Relayed  bool      `json:"relayed,omitempty"`
	Defaults bool      `json:"defaults,omitempty"`
	At       time.Time `json:"at"`
}

// PlanPR is one entry of the plan's PR split.
type PlanPR struct {
	Title    string   `json:"title"`
	Files    []string `json:"files,omitempty"`
	Tests    string   `json:"tests,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Consumes []string `json:"consumes,omitempty"`
	Produces []string `json:"produces,omitempty"`
}

// BacklogPlan is the plan before build for one backlog item. The database
// record is the source of truth; an artifact copy is informational.
type BacklogPlan struct {
	ID               string
	ProjectID        ProjectID
	ItemID           string
	Status           PlanStatus
	PlannerSessionID SessionID
	// NotifySessionID is the orchestrator that started the plan; its lifecycle
	// notices go there. Empty when the owner started it from the UI.
	NotifySessionID SessionID
	BaseCommit      string
	BaseBranch      string
	Questions       []PlanQuestion
	Assumptions     []PlanAssumption
	Answers         *PlanAnswers
	PlanMarkdown    string
	DecisionsMD     string
	PRSplit         []PlanPR
	ReviewFocus     []string
	ApprovedBy      string
	ApprovedAt      time.Time
	OwnerMessageID  string
	BuildAnyway     bool
	ItemUpdatedAt   time.Time
	// ItemChangedAt is when the item's title, note or done-when was edited
	// while the plan was open or approved; zero when untouched.
	ItemChangedAt time.Time
	StartedAt     time.Time
	UpdatedAt     time.Time
	Failure       string
}

// ValidatePlanQuestions enforces the question and assumption caps.
func ValidatePlanQuestions(qs []PlanQuestion, as []PlanAssumption) error {
	if len(qs) > MaxPlanQuestions {
		return fmt.Errorf("at most %d questions are allowed, got %d", MaxPlanQuestions, len(qs))
	}
	if len(as) > MaxPlanAssumptions {
		return fmt.Errorf("at most %d assumptions are allowed, got %d", MaxPlanAssumptions, len(as))
	}
	seen := map[string]bool{}
	for i, q := range qs {
		if strings.TrimSpace(q.ID) == "" || seen[q.ID] {
			return fmt.Errorf("question %d needs a unique id", i+1)
		}
		seen[q.ID] = true
		if strings.TrimSpace(q.Text) == "" {
			return fmt.Errorf("question %s has no text", q.ID)
		}
		if utf8.RuneCountInString(q.Text) > MaxPlanQuestionChars {
			return fmt.Errorf("question %s is over %d characters", q.ID, MaxPlanQuestionChars)
		}
		if len(q.Options) > MaxPlanOptions {
			return fmt.Errorf("question %s has more than %d options", q.ID, MaxPlanOptions)
		}
		if len(q.Options) == 0 {
			return fmt.Errorf("question %s needs at least one option", q.ID)
		}
		found := false
		for _, o := range q.Options {
			if strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.Label) == "" {
				return fmt.Errorf("question %s has an option without id or label", q.ID)
			}
			if utf8.RuneCountInString(o.Label) > MaxPlanOptionLabelChars {
				return fmt.Errorf("question %s has an option label over %d characters", q.ID, MaxPlanOptionLabelChars)
			}
			if o.ID == q.Recommended {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("question %s: recommended %q is not one of its options", q.ID, q.Recommended)
		}
	}
	seen = map[string]bool{}
	for i, a := range as {
		if strings.TrimSpace(a.ID) == "" || seen[a.ID] {
			return fmt.Errorf("assumption %d needs a unique id", i+1)
		}
		seen[a.ID] = true
		if strings.TrimSpace(a.Text) == "" {
			return fmt.Errorf("assumption %s has no text", a.ID)
		}
		if utf8.RuneCountInString(a.Text) > MaxPlanAssumptionChars {
			return fmt.Errorf("assumption %s is over %d characters", a.ID, MaxPlanAssumptionChars)
		}
	}
	return nil
}

// ValidatePlanSubmission enforces the plan text, decisions, PR split and review
// focus caps. Every PR entry must name what it produces or consumes.
func ValidatePlanSubmission(plan, decisions string, prs []PlanPR, focus []string) error {
	if strings.TrimSpace(plan) == "" {
		return fmt.Errorf("the plan markdown is empty")
	}
	if len(plan) > MaxPlanMarkdownBytes {
		return fmt.Errorf("the plan is over %d bytes", MaxPlanMarkdownBytes)
	}
	if len(decisions) > MaxPlanDecisionsBytes {
		return fmt.Errorf("the decisions table is over %d bytes", MaxPlanDecisionsBytes)
	}
	if len(prs) == 0 {
		return fmt.Errorf("the PR split needs at least one entry")
	}
	if len(prs) > MaxPlanPRs {
		return fmt.Errorf("at most %d PR entries are allowed, got %d", MaxPlanPRs, len(prs))
	}
	for i, p := range prs {
		if strings.TrimSpace(p.Title) == "" {
			return fmt.Errorf("PR entry %d has no title", i+1)
		}
		if len(p.Files) > MaxPlanPRFiles {
			return fmt.Errorf("PR entry %d names more than %d files", i+1, MaxPlanPRFiles)
		}
		if len(p.Produces) == 0 && len(p.Consumes) == 0 {
			return fmt.Errorf("PR entry %d (%s) needs non-empty produces or consumes", i+1, p.Title)
		}
	}
	if len(focus) > MaxPlanReviewFocus {
		return fmt.Errorf("at most %d review focus items are allowed, got %d", MaxPlanReviewFocus, len(focus))
	}
	for i, f := range focus {
		if utf8.RuneCountInString(f) > MaxPlanFocusChars {
			return fmt.Errorf("review focus item %d is over %d characters", i+1, MaxPlanFocusChars)
		}
	}
	return nil
}
