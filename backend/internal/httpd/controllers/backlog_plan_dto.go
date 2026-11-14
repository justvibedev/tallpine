package controllers

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// PlanEnvelope wraps one plan.
type PlanEnvelope struct {
	Plan PlanView `json:"plan"`
}

// PlanQuestionsRequest is the planner's first submission.
type PlanQuestionsRequest struct {
	Questions   []PlanQuestionDTO   `json:"questions"`
	Assumptions []PlanAssumptionDTO `json:"assumptions"`
}

// PlanAnswersRequest answers the questions and corrects assumptions.
type PlanAnswersRequest struct {
	Questions   map[string]PlanAnswerDTO `json:"questions"`
	Corrections map[string]string        `json:"corrections,omitempty"`
	Relayed     bool                     `json:"relayed,omitempty" description:"The orchestrator passes on what the owner typed. Forced on for an orchestrator."`
}

// PlanDefaultsRequest accepts every recommendation and assumption.
type PlanDefaultsRequest struct {
	Relayed bool `json:"relayed,omitempty"`
}

// PlanSubmitRequest is the finished plan.
type PlanSubmitRequest struct {
	Plan      string      `json:"plan" description:"Plan markdown."`
	Decisions string      `json:"decisions" description:"Decisions table markdown."`
	PRs       []PlanPRDTO `json:"prs"`
	Focus     []string    `json:"focus,omitempty" description:"At most 5 failure modes the reviewer checks hardest."`
}

// PlanApproveRequest approves a plan. An orchestrator names the owner's message.
type PlanApproveRequest struct {
	OwnerMessage string `json:"ownerMessage,omitempty" description:"Owner chat message id, or last. Required for an orchestrator."`
}

// ProjectPlanDTO is a plan with the backlog item it belongs to.
type ProjectPlanDTO struct {
	ItemRef   string   `json:"itemRef"`
	ItemTitle string   `json:"itemTitle"`
	Plan      PlanView `json:"plan"`
	// Lane is the lane whose orchestrator started the plan; empty for the main orchestrator or the owner.
	Lane string `json:"lane,omitempty"`
}

// ProjectPlansResponse lists the project's plans that wait on someone.
type ProjectPlansResponse struct {
	Plans []ProjectPlanDTO `json:"plans"`
}

func planView(p domain.BacklogPlan) PlanView {
	v := PlanView{
		ID: p.ID, ItemID: p.ItemID, Status: string(p.Status), PlannerSessionID: string(p.PlannerSessionID), StarterSessionID: string(p.NotifySessionID),
		BaseCommit: p.BaseCommit, BaseBranch: p.BaseBranch, PlanMarkdown: p.PlanMarkdown, DecisionsMarkdown: p.DecisionsMD,
		ApprovedBy: p.ApprovedBy, BuildAnyway: p.BuildAnyway, StartedAt: p.StartedAt, UpdatedAt: p.UpdatedAt, Failure: p.Failure, ItemChanged: !p.ItemChangedAt.IsZero(),
		Questions: []PlanQuestionDTO{}, Assumptions: []PlanAssumptionDTO{}, PRSplit: []PlanPRDTO{}, ReviewFocus: append([]string{}, p.ReviewFocus...),
	}
	if !p.ApprovedAt.IsZero() {
		t := p.ApprovedAt
		v.ApprovedAt = &t
	}
	for _, q := range p.Questions {
		dto := PlanQuestionDTO{ID: q.ID, Text: q.Text, Reason: q.Reason, Recommended: q.Recommended, Options: []PlanOptionDTO{}}
		for _, o := range q.Options {
			dto.Options = append(dto.Options, PlanOptionDTO{ID: o.ID, Label: o.Label})
		}
		v.Questions = append(v.Questions, dto)
	}
	for _, a := range p.Assumptions {
		v.Assumptions = append(v.Assumptions, PlanAssumptionDTO{ID: a.ID, Text: a.Text, ScopeCut: a.ScopeCut})
	}
	for _, pr := range p.PRSplit {
		v.PRSplit = append(v.PRSplit, PlanPRDTO(pr))
	}
	if a := p.Answers; a != nil {
		out := &PlanAnswersDTO{Questions: map[string]PlanAnswerDTO{}, Corrections: a.Corrections, By: a.By, Relayed: a.Relayed, Defaults: a.Defaults, At: a.At}
		for id, ans := range a.Questions {
			out.Questions[id] = PlanAnswerDTO(ans)
		}
		v.Answers = out
	}
	return v
}

func (r PlanQuestionsRequest) toInput() (qs []domain.PlanQuestion, as []domain.PlanAssumption) {
	for _, q := range r.Questions {
		dq := domain.PlanQuestion{ID: q.ID, Text: q.Text, Reason: q.Reason, Recommended: q.Recommended}
		for _, o := range q.Options {
			dq.Options = append(dq.Options, domain.PlanOption(o))
		}
		qs = append(qs, dq)
	}
	for _, a := range r.Assumptions {
		as = append(as, domain.PlanAssumption(a))
	}
	return qs, as
}
