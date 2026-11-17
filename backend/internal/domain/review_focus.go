package domain

import "unicode/utf8"

// ReviewFocusAuthor is who gave a pass's one-off review focus. It is derived
// from the request's credentials, never from the request body, because the
// reviewer frames the focus differently by author.
type ReviewFocusAuthor string

const (
	// ReviewFocusOwner marks focus a person gave (no agent token).
	ReviewFocusOwner ReviewFocusAuthor = "owner"
	// ReviewFocusOrchestrator marks focus from the project's orchestrator token.
	ReviewFocusOrchestrator ReviewFocusAuthor = "orchestrator"
	// ReviewFocusWorker marks focus from a worker token: the author of the code
	// under review, so the reviewer treats it as an untrusted hint.
	ReviewFocusWorker ReviewFocusAuthor = "worker"
)

// MaxReviewFocusChars bounds a pass's one-off review focus, in characters.
const MaxReviewFocusChars = 2000

// IsKnown reports whether a is one of the defined focus authors.
func (a ReviewFocusAuthor) IsKnown() bool {
	switch a {
	case ReviewFocusOwner, ReviewFocusOrchestrator, ReviewFocusWorker:
		return true
	}
	return false
}

// Trusted reports whether focus from a may direct the reviewer (owner or
// orchestrator). Worker focus, and focus of unknown origin, is only a hint.
func (a ReviewFocusAuthor) Trusted() bool {
	return a == ReviewFocusOwner || a == ReviewFocusOrchestrator
}

// ReviewFocusTooLong reports whether focus exceeds MaxReviewFocusChars.
func ReviewFocusTooLong(focus string) bool {
	return utf8.RuneCountInString(focus) > MaxReviewFocusChars
}
