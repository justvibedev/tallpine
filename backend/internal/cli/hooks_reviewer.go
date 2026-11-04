package cli

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/review"
)

// claudePermissionHookOutput is Claude Code's PermissionRequest decision: the
// hook answers the prompt in place of a human.
type claudePermissionHookOutput struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

const reviewerPermissionDenyMessage = "Tallpine headless reviewer: no human can answer permission prompts. " +
	"Use only the allowlisted read commands (git diff/log/show/status, gh pr view/diff/checks, Read, Grep, Glob) " +
	"and the exact single-line `printf '%s' '<json>' | ...` submit commands from the review task."

// reviewerPermissionDecision answers a Claude Code reviewer's PermissionRequest:
// allow the exact submit shapes, deny everything else so the session degrades
// into a denial the model can route around instead of an unanswered prompt.
func reviewerPermissionDecision(payload []byte, workerSessionID, allowedPRs string) claudePermissionHookOutput {
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	_ = json.Unmarshal(payload, &p)
	var out claudePermissionHookOutput
	out.HookSpecificOutput.HookEventName = "PermissionRequest"
	out.HookSpecificOutput.Decision.Behavior = "deny"
	out.HookSpecificOutput.Decision.Message = reviewerPermissionDenyMessage
	if p.ToolName != "Bash" {
		return out
	}
	submit, ok := review.MatchSubmitCommand(p.ToolInput.Command)
	if !ok {
		return out
	}
	if workerSessionID == "" || (submit.Session != "" && submit.Session != workerSessionID) {
		// An unset TALLPINE_REVIEW_WORKER_SESSION_ID means a broken launch (the
		// launcher always sets it); admit nothing rather than any worker.
		return out
	}
	if submit.IsGitHubReview() && (!submit.PRAllowed(allowedPRs) || !submit.EventAllowed()) {
		return out
	}
	out.HookSpecificOutput.Decision.Behavior = "allow"
	out.HookSpecificOutput.Decision.Message = ""
	return out
}
