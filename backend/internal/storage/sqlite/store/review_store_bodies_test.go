package store_test

import (
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// wantUnsubmittedBody is the failure body a settled reviewer Chat turn leaves on
// a pass that submitted nothing.
func wantUnsubmittedBody(state domain.TurnState, errMsg string) string {
	switch {
	case errMsg != "" && strings.Contains(errMsg, "hit your"):
		return "reviewer usage limit: " + errMsg
	case errMsg != "":
		return "reviewer Chat turn failed: " + errMsg
	case state == domain.TurnStateInterrupted:
		return "reviewer Chat turn interrupted by a restart"
	}
	return "reviewer Chat turn ended without submitting a result"
}
