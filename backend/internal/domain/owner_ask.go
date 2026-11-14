package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// AskState is where a tracked owner ask stands.
type AskState string

const (
	// AskOpen waits on the owner.
	AskOpen AskState = "open"
	// AskAnswered was answered by the owner, on the card or in chat.
	AskAnswered AskState = "answered"
	// AskWithdrawn was dropped by its orchestrator (or its lane ended).
	AskWithdrawn AskState = "withdrawn"
)

// Valid reports whether s is a known state.
func (s AskState) Valid() bool {
	switch s {
	case AskOpen, AskAnswered, AskWithdrawn:
		return true
	}
	return false
}

// Limits on a tracked ask, in the shape of a plan question.
const (
	MinAskOptions     = 2
	MaxAskOptions     = MaxPlanOptions
	MaxAskTextChars   = MaxPlanQuestionChars
	MaxAskReasonChars = 500
	MaxAskAboutChars  = 64
	// MaxOpenAsks caps the open asks of one project/lane chat so the strip
	// stays usable.
	MaxOpenAsks = 10
)

// AskAnswer is how the owner answered. Exactly one of OptionID and Other is set.
type AskAnswer struct {
	OptionID string `json:"optionId,omitempty"`
	Other    string `json:"other,omitempty"`
	// Text is what the owner chose, for display: the option label or the free text.
	Text string `json:"text"`
	// Relayed marks an answer the orchestrator recorded from a chat message.
	Relayed bool `json:"relayed,omitempty"`
	// MessageID is the owner chat message a relayed answer rests on.
	MessageID string `json:"messageId,omitempty"`
}

// OwnerAsk is a question an orchestrator put to the owner, tracked by the
// daemon. It belongs to a project and lane, not to one session, so a restarted
// orchestrator still answers it.
type OwnerAsk struct {
	ID        string
	ProjectID ProjectID
	// Ref is the per-project reference the owner and orchestrators use: Q1, Q2.
	Ref string
	// Lane is the lane item id of the asking chat; "" is the project's main chat.
	Lane string
	// LaneRef is the backlog ref of that lane (B7), the label clients know the lane by.
	LaneRef string
	// SessionID is the orchestrator that asked (informational).
	SessionID   SessionID
	Text        string
	Reason      string
	Options     []PlanOption
	Recommended string
	// About is an optional key (such as a backlog ref) for dedupe.
	About string
	State AskState
	// Answer is set once answered.
	Answer *AskAnswer
	// ReaskOf is the ref of the earlier ask this one repeats on purpose, with
	// ReaskReason the orchestrator's why.
	ReaskOf        string
	ReaskReason    string
	WithdrawReason string
	// AnchorTurnID and AnchorActivityID locate the inline card on the asking
	// turn; empty when no turn was in flight.
	AnchorTurnID     string
	AnchorActivityID string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	AnsweredAt       time.Time
}

// Anchored reports whether the ask has an inline card to jump to.
func (a OwnerAsk) Anchored() bool { return a.AnchorActivityID != "" }

// NormalizeAskText folds an ask's text for dedupe: lower case, punctuation
// and runs of space removed, so a rewording of case or spacing still matches.
func NormalizeAskText(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case unicode.IsSpace(r):
			space = true
		}
	}
	return b.String()
}

// Matches reports whether b repeats a: the same non-empty About key or the same
// normalized text.
func (a OwnerAsk) Matches(about, text string) bool {
	if about != "" && strings.EqualFold(a.About, about) {
		return true
	}
	return NormalizeAskText(a.Text) == NormalizeAskText(text)
}

// ValidateAsk checks a new ask's text, options and recommendation.
func ValidateAsk(text, reason, about string, opts []PlanOption, recommended, reaskReason string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("the question has no text")
	}
	if utf8.RuneCountInString(text) > MaxAskTextChars {
		return fmt.Errorf("the question is over %d characters", MaxAskTextChars)
	}
	if utf8.RuneCountInString(reason) > MaxAskReasonChars {
		return fmt.Errorf("the reason is over %d characters", MaxAskReasonChars)
	}
	if utf8.RuneCountInString(reaskReason) > MaxAskReasonChars {
		return fmt.Errorf("the --reask reason is over %d characters", MaxAskReasonChars)
	}
	if utf8.RuneCountInString(about) > MaxAskAboutChars {
		return fmt.Errorf("--about is over %d characters", MaxAskAboutChars)
	}
	if len(opts) < MinAskOptions || len(opts) > MaxAskOptions {
		return fmt.Errorf("an ask needs %d to %d options, got %d", MinAskOptions, MaxAskOptions, len(opts))
	}
	seen := map[string]bool{}
	found := false
	for _, o := range opts {
		if strings.TrimSpace(o.ID) == "" || strings.TrimSpace(o.Label) == "" {
			return fmt.Errorf("an option has no id or label")
		}
		if seen[o.ID] {
			return fmt.Errorf("option id %q is used twice", o.ID)
		}
		seen[o.ID] = true
		if utf8.RuneCountInString(o.Label) > MaxPlanOptionLabelChars {
			return fmt.Errorf("an option label is over %d characters", MaxPlanOptionLabelChars)
		}
		found = found || o.ID == recommended
	}
	if !found {
		return fmt.Errorf("recommended %q is not one of the options", recommended)
	}
	return nil
}

// ResolveAskAnswer turns an option id or free text into an AskAnswer. Exactly
// one of optionID and other must be given.
func (a OwnerAsk) ResolveAskAnswer(optionID, other string) (AskAnswer, error) {
	optionID, other = strings.TrimSpace(optionID), strings.TrimSpace(other)
	switch {
	case optionID != "" && other != "":
		return AskAnswer{}, fmt.Errorf("give an option or free text, not both")
	case optionID == "" && other == "":
		return AskAnswer{}, fmt.Errorf("give an option or free text")
	case other != "":
		if utf8.RuneCountInString(other) > MaxPlanAnswerChars {
			return AskAnswer{}, fmt.Errorf("the answer is over %d characters", MaxPlanAnswerChars)
		}
		return AskAnswer{Other: other, Text: other}, nil
	}
	for _, o := range a.Options {
		if o.ID == optionID {
			return AskAnswer{OptionID: o.ID, Text: o.Label}, nil
		}
	}
	return AskAnswer{}, fmt.Errorf("%q is not one of %s's options", optionID, a.Ref)
}

// ErrAskNotOpen reports a transition on an ask that is no longer open.
var ErrAskNotOpen = errors.New("ask is not open")

// Answered returns the ask answered: the only way out of open besides
// withdrawal. It fails with ErrAskNotOpen on any other state.
func (a OwnerAsk) Answered(ans AskAnswer, now time.Time) (OwnerAsk, error) {
	if a.State != AskOpen {
		return a, ErrAskNotOpen
	}
	a.State, a.Answer, a.AnsweredAt, a.UpdatedAt = AskAnswered, &ans, now, now
	return a, nil
}

// Withdrawn returns the ask withdrawn with a reason. It fails with
// ErrAskNotOpen on any other state.
func (a OwnerAsk) Withdrawn(reason string, now time.Time) (OwnerAsk, error) {
	if a.State != AskOpen {
		return a, ErrAskNotOpen
	}
	a.State, a.WithdrawReason, a.UpdatedAt = AskWithdrawn, reason, now
	return a, nil
}
