package apitypes

import (
	"time"
)

// PlanAnswerDTO answers one question: an option id, or free text (Other).
type PlanAnswerDTO struct {
	Option string `json:"option,omitempty"`
	Other  string `json:"other,omitempty"`
}

// PlanAnswersDTO is what the owner, or the orchestrator relaying the owner, answered.
type PlanAnswersDTO struct {
	Questions   map[string]PlanAnswerDTO `json:"questions"`
	Corrections map[string]string        `json:"corrections,omitempty"`
	By          string                   `json:"by" description:"user, or orchestrator:<session id>."`
	Relayed     bool                     `json:"relayed,omitempty"`
	Defaults    bool                     `json:"defaults,omitempty"`
	At          time.Time                `json:"at"`
}

// PlanAssumptionDTO is a one-line assumption the owner accepts by default.
type PlanAssumptionDTO struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	ScopeCut bool   `json:"scopeCut,omitempty"`
}

// PlanOptionDTO is one answer choice of a plan question.
type PlanOptionDTO struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// PlanPRDTO is one entry of the plan's PR split.
type PlanPRDTO struct {
	Title    string   `json:"title"`
	Files    []string `json:"files,omitempty"`
	Tests    string   `json:"tests,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Consumes []string `json:"consumes,omitempty"`
	Produces []string `json:"produces,omitempty"`
}

// PlanQuestionDTO is a question for the owner with the planner's recommendation.
type PlanQuestionDTO struct {
	ID          string          `json:"id"`
	Text        string          `json:"text"`
	Reason      string          `json:"reason,omitempty"`
	Options     []PlanOptionDTO `json:"options"`
	Recommended string          `json:"recommended" description:"Id of the recommended option."`
}

// PlanView is a backlog item's plan: questions, assumptions, answers, the plan
// text, decisions table and PR split.
type PlanView struct {
	ID                string              `json:"id"`
	ItemID            string              `json:"itemId"`
	Status            string              `json:"status" enum:"drafting,awaiting_answers,writing,awaiting_approval,approved,superseded,failed"`
	PlannerSessionID  string              `json:"plannerSessionId,omitempty"`
	StarterSessionID  string              `json:"starterSessionId,omitempty" description:"The orchestrator that started the plan; empty when the owner started it."`
	BaseCommit        string              `json:"baseCommit,omitempty"`
	BaseBranch        string              `json:"baseBranch,omitempty"`
	Questions         []PlanQuestionDTO   `json:"questions"`
	Assumptions       []PlanAssumptionDTO `json:"assumptions"`
	Answers           *PlanAnswersDTO     `json:"answers,omitempty"`
	PlanMarkdown      string              `json:"planMarkdown,omitempty"`
	DecisionsMarkdown string              `json:"decisionsMarkdown,omitempty"`
	PRSplit           []PlanPRDTO         `json:"prSplit"`
	ReviewFocus       []string            `json:"reviewFocus"`
	ApprovedBy        string              `json:"approvedBy,omitempty"`
	ApprovedAt        *time.Time          `json:"approvedAt,omitempty"`
	BuildAnyway       bool                `json:"buildAnyway,omitempty"`
	StartedAt         time.Time           `json:"startedAt"`
	UpdatedAt         time.Time           `json:"updatedAt"`
	Failure           string              `json:"failure,omitempty"`
	Stale             bool                `json:"stale,omitempty" description:"Approved plan whose named files changed on the default branch since its base commit."`
	ItemChanged       bool                `json:"itemChanged,omitempty" description:"The item title, note or done-when was edited after planning began."`
	PausedUntil       *time.Time          `json:"pausedUntil,omitempty" description:"The planner's provider account is out of quota until this time; it resumes by itself then. Set only while the plan is drafting or writing."`
}
