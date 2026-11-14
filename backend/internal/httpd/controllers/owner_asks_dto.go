package controllers

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// OwnerAskView is a tracked question an orchestrator put to the owner.
type OwnerAskView struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"projectId"`
	Ref         string          `json:"ref" description:"Per-project reference: Q1, Q2."`
	Lane        string          `json:"lane,omitempty" description:"Backlog ref of the lane of the asking chat (B7); empty for the main chat."`
	SessionID   string          `json:"sessionId,omitempty" description:"The orchestrator that asked."`
	Text        string          `json:"text"`
	Reason      string          `json:"reason,omitempty"`
	Options     []PlanOptionDTO `json:"options"`
	Recommended string          `json:"recommended" description:"Id of the recommended option."`
	About       string          `json:"about,omitempty"`
	State       string          `json:"state" enum:"open,answered,withdrawn"`
	Answer      *OwnerAskAnswer `json:"answer,omitempty"`
	ReaskOf     string          `json:"reaskOf,omitempty" description:"Ref of the earlier answered ask this one repeats."`
	ReaskReason string          `json:"reaskReason,omitempty"`
	// WithdrawReason says why a withdrawn ask was dropped.
	WithdrawReason string `json:"withdrawReason,omitempty"`
	// Anchored is true when the ask has an inline card on the asking turn to jump to.
	Anchored   bool       `json:"anchored"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	AnsweredAt *time.Time `json:"answeredAt,omitempty"`
}

// OwnerAskAnswer is how the owner answered: an option or free text.
type OwnerAskAnswer struct {
	OptionID string `json:"optionId,omitempty"`
	Other    string `json:"other,omitempty"`
	Text     string `json:"text" description:"What the owner chose: the option label or the free text."`
	Relayed  bool   `json:"relayed,omitempty" description:"The orchestrator recorded it from a chat message."`
}

// OwnerAsksResponse lists a project's asks, oldest first.
type OwnerAsksResponse struct {
	Asks []OwnerAskView `json:"asks"`
}

// OwnerAskEnvelope wraps one ask.
type OwnerAskEnvelope struct {
	Ask OwnerAskView `json:"ask"`
	// DeliveryFailed is set on a card answer that was recorded but could not be posted to a live orchestrator.
	DeliveryFailed bool `json:"deliveryFailed,omitempty"`
}

// OwnerAskListQuery filters GET /projects/{projectId}/asks.
type OwnerAskListQuery struct {
	State string `query:"state,omitempty" enum:"open,all" description:"open (default) lists only asks waiting on the owner; all adds answered and withdrawn ones."`
}

// OwnerAskRefParam is the {ref} path parameter of an ask.
type OwnerAskRefParam struct {
	Ref string `path:"ref" description:"Ask ref (Q3)."`
}

// OwnerAskCreateRequest files an ask (orchestrators only).
type OwnerAskCreateRequest struct {
	Text        string          `json:"text" description:"The question, at most 500 characters."`
	Reason      string          `json:"reason,omitempty" description:"Why the owner is asked."`
	About       string          `json:"about,omitempty" description:"Dedupe key such as a backlog ref (B62)."`
	Options     []PlanOptionDTO `json:"options" description:"2 to 6 answer choices."`
	Recommended string          `json:"recommended" description:"Id of the recommended option."`
	Reask       string          `json:"reask,omitempty" description:"Why asking again is needed, to repeat an answered question."`
}

// OwnerAskAnswerRequest answers an ask: an option id or free text. An
// orchestrator also names the owner message the answer rests on.
type OwnerAskAnswerRequest struct {
	Option       string `json:"option,omitempty" description:"Id of the chosen option."`
	Other        string `json:"other,omitempty" description:"Free-text answer instead of an option."`
	OwnerMessage string `json:"ownerMessage,omitempty" description:"Owner chat message id, or last. Required for an orchestrator."`
}

// OwnerAskWithdrawRequest drops an open ask.
type OwnerAskWithdrawRequest struct {
	Reason string `json:"reason,omitempty"`
}

func ownerAskView(a domain.OwnerAsk) OwnerAskView {
	v := OwnerAskView{
		ID: a.ID, ProjectID: string(a.ProjectID), Ref: a.Ref, Lane: a.LaneRef, SessionID: string(a.SessionID),
		Text: a.Text, Reason: a.Reason, Recommended: a.Recommended, About: a.About, State: string(a.State),
		ReaskOf: a.ReaskOf, ReaskReason: a.ReaskReason, WithdrawReason: a.WithdrawReason, Anchored: a.Anchored(),
		CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt, Options: []PlanOptionDTO{},
	}
	for _, o := range a.Options {
		v.Options = append(v.Options, PlanOptionDTO{ID: o.ID, Label: o.Label})
	}
	if a.Answer != nil {
		v.Answer = &OwnerAskAnswer{OptionID: a.Answer.OptionID, Other: a.Answer.Other, Text: a.Answer.Text, Relayed: a.Answer.Relayed}
	}
	if !a.AnsweredAt.IsZero() {
		t := a.AnsweredAt
		v.AnsweredAt = &t
	}
	return v
}
