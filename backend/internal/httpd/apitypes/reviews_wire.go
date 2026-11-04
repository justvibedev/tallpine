package apitypes

import (
	"tallpine.eu/tallpine/backend/internal/domain"
)

// TriggerReviewRequest is the optional body of the review trigger route. An
// empty harness keeps the project's configured reviewer; setting one overrides
// it for this pass only, without editing project config, so one session's choice
// cannot change what another session in the project runs.
type TriggerReviewRequest struct {
	Harness       domain.ReviewerHarness       `json:"harness,omitempty" enum:"claude-code,codex,kiro,deepseek-harness"`
	AgentConfig   domain.AgentConfig           `json:"agentConfig,omitempty"`
	InterfaceMode domain.ReviewerInterfaceMode `json:"interfaceMode,omitempty" enum:"chat,tui"`
	PRURL         string                       `json:"prUrl,omitempty" description:"Restrict the pass to this pull request, attaching it to the session first if Tallpine does not track it yet (never from another active session). Omit to review every eligible PR on the session."`
	Head          string                       `json:"head,omitempty" description:"Commit SHA (full or abbreviated) the caller expects the PR to be at. The PR is refreshed from the provider first; 409 when its head differs, so a push Tallpine has not seen yet is never reviewed as the previous commit."`
	// Source labels who asked for the pass. Omitted means a person (manual).
	Source string `json:"source,omitempty" enum:"manual,agent" description:"Who requested the pass: manual (a person, the default) or agent (a Tallpine session through the CLI)."`
	// RejectReviewedHead is the CLI's same-commit policy. Omitting it keeps the
	// reuse behavior the desktop app relies on.
	RejectReviewedHead bool `json:"rejectReviewedHead,omitempty" description:"Return 409 instead of reusing when every open PR head is already being reviewed or already has a review."`
	Rerun              bool `json:"rerun,omitempty" description:"Start a fresh pass for already-reviewed current heads; a different reviewer may run alongside one that is still running. A person's rerun reuses an active pass from the same reviewer; an agent's rerun returns 409 REVIEW_ALREADY_RUNNING instead."`
	EnableAutoInject   bool `json:"enableAutoInject,omitempty" description:"Turn on the worker session's review auto-inject once a pass has started, so the reviewer's PR review comments reach the worker."`
}
