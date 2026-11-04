package lifecycle

import (
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// normalizeActivitySignal trims the wire fields of an activity signal, drops
// child-agent identity from root-conversation fields, and keeps provider text
// only for the events that own it. ok is false when the signal carries nothing
// the lifecycle may record.
func normalizeActivitySignal(s ports.ActivitySignal) (_ ports.ActivitySignal, ok bool) {
	// Subagent answers, including prompt suggestions, are not root-conversation
	// facts. Their usage is collected independently from lifecycle metadata.
	s.SubagentID = strings.TrimSpace(s.SubagentID)
	if s.Event == "subagent-stop" && s.SubagentID == "" {
		return s, false
	}
	s.AgentSessionID = strings.TrimSpace(s.AgentSessionID)
	s.LatestUserPrompt = strings.TrimSpace(s.LatestUserPrompt)
	s.LatestAssistantUpdate = strings.TrimSpace(s.LatestAssistantUpdate)
	s.TranscriptPath = strings.TrimSpace(s.TranscriptPath)
	s.LaunchID = strings.TrimSpace(s.LaunchID)
	s.ControllerGeneration = strings.TrimSpace(s.ControllerGeneration)
	// A non-interactive provider process (e.g. a summarizer's `claude -p` run in
	// the session worktree) inherits TALLPINE_SESSION_ID and TALLPINE_RUNTIME_LAUNCH_ID, so
	// the launch-generation fence below cannot reject it. Tallpine never launches such
	// a process as a hook-reporting session runtime, and Chat controllers report
	// through ControllerGeneration rather than hooks. Its identity, transcript,
	// and SessionEnd must not replace the live runtime's facts (#6454).
	if s.ControllerGeneration == "" && nonInteractiveNativeEntrypoint(s.NativeEntrypoint) {
		return s, false
	}
	s.ProviderTurnID = strings.TrimSpace(s.ProviderTurnID)
	s.SubmissionID = strings.TrimSpace(s.SubmissionID)
	if s.SubagentID != "" {
		// Child hook identity and transcript are not the root conversation's
		// resumable identity or history checkpoint.
		s.AgentSessionID = ""
		s.TranscriptPath = ""
		s.LatestUserPrompt = ""
		s.LatestAssistantUpdate = ""
		s.FinalAssistantMessage = ""
		s.TurnUserRequest = ""
		s.ProviderTurnID = ""
	}
	if !s.ConversationCheckpointOrigin.Valid() {
		s.ConversationCheckpointOrigin = domain.ConversationCheckpointOriginUnknown
	}
	// The hook event is the provenance boundary for provider text. Native hook
	// payloads repeat similarly named aliases on tool, lifecycle, and subagent
	// events, so accepting a field merely because it is non-empty can promote an
	// unrelated message into a hard native-history checkpoint. Missing event
	// provenance is untrusted as well: these text fields and Event were introduced
	// together on Tallpine's activity wire contract.
	switch s.Event {
	case "user-prompt-submit":
		s.LatestAssistantUpdate = ""
	case "stop":
		s.LatestUserPrompt = ""
	default:
		s.LatestUserPrompt = ""
		s.LatestAssistantUpdate = ""
		s.ProviderTurnID = ""
	}
	if !s.Valid && s.SubagentID == "" && s.AgentSessionID == "" && s.LatestUserPrompt == "" && s.LatestAssistantUpdate == "" && s.TranscriptPath == "" {
		return s, false
	}
	return s, true
}

// checkpointOwner is the identity a conversation checkpoint belongs to: the
// harness, the owning launch or controller generation, and the native
// conversation id, plus whether this signal reset the checkpoint or sits on a
// Claude native prompt boundary.
type checkpointOwner struct {
	harness        domain.AgentHarness
	generation     string
	nativeID       string
	reset          bool
	claudeBoundary bool
}

// applyPromptSubmitCheckpoint folds a user-prompt-submit signal into the
// checkpoint and reports whether the prompt opened a new turn.
func applyPromptSubmitCheckpoint(checkpoint *domain.SessionMetadata, s ports.ActivitySignal, o checkpointOwner, now time.Time) (accepted bool) {
	if s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination {
		accepted = true
		// Preserve the last real human facts and their timestamp, but durably
		// mark this owner turn as coordination so a promptless Stop cannot
		// promote its response. Chat still checks the preceding human text.
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointCoordination
		checkpoint.ConversationCheckpointGeneration = o.generation
		checkpoint.ConversationCheckpointNativeID = o.nativeID
		checkpoint.ConversationCheckpointTurnID = s.CoordinationID
	} else {
		promptAt := timeOr(s.Timestamp, now)
		sameCheckpointOwner := !o.reset && o.generation != "" &&
			checkpoint.ConversationCheckpointGeneration == o.generation &&
			checkpoint.ConversationCheckpointNativeID == o.nativeID
		if sameCheckpointOwner && (promptAt.Before(checkpoint.LatestUserPromptAt) ||
			(promptAt.Equal(checkpoint.LatestUserPromptAt) && s.LatestUserPrompt == checkpoint.LatestUserPrompt)) {
			// A delayed/duplicate prompt must not replace a newer coherent
			// checkpoint or clear its answer. Equal-time different text still
			// establishes a new boundary; timestamps alone cannot order it.
			if s.LatestUserPrompt != checkpoint.LatestUserPrompt {
				// The timestamp may be a prior owner's high-water mark after
				// clock skew. Do not let this prompt's Stop validate old text.
				checkpoint.ConversationCheckpointUnsettled = true
			}
			return accepted
		}
		accepted = true
		checkpoint.LatestUserPrompt = s.LatestUserPrompt
		if promptAt.After(checkpoint.LatestUserPromptAt) {
			// Owner changes can carry skewed clocks. Preserve the last-human
			// high-water time while replacing all checkpoint text/provenance.
			checkpoint.LatestUserPromptAt = promptAt
		}
		checkpoint.LatestAssistantUpdate = ""
		checkpoint.LatestAssistantUpdateAt = time.Time{}
		checkpoint.ConversationCheckpointUnsettled = false
		checkpoint.ConversationCheckpointGeneration = ""
		checkpoint.ConversationCheckpointNativeID = ""
		checkpoint.ConversationCheckpointTurnID = ""
		if o.generation != "" && o.nativeID != "" {
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointPrompt
			checkpoint.ConversationCheckpointGeneration = o.generation
			checkpoint.ConversationCheckpointNativeID = o.nativeID
			if o.harness == domain.HarnessCodex {
				checkpoint.ConversationCheckpointTurnID = s.ProviderTurnID
			}
		} else {
			// An unowned event is retained conservatively for an ordinary strict
			// switch, but explicit provider-history recovery may identify it as
			// untrusted. It must not be upgraded by a later Stop.
			checkpoint.ConversationCheckpointState = domain.ConversationCheckpointLegacy
		}
	}
	return accepted
}

// applyStopCheckpoint folds a stop signal into the checkpoint. It clears the
// signal's assistant text when that text must not be promoted.
func applyStopCheckpoint(checkpoint *domain.SessionMetadata, s *ports.ActivitySignal, o checkpointOwner, now time.Time) {
	if o.claudeBoundary {
		// A queued UserPromptSubmit reuses the executing prompt_id. Never certify
		// its text with this Stop's answer; native ancestry resolves the pair at
		// handoff, once the source is conclusively stopped.
		checkpoint.ConversationCheckpointUnsettled = true
		s.LatestAssistantUpdate = ""
		return
	}
	coordinationStop := s.ConversationCheckpointOrigin == domain.ConversationCheckpointOriginCoordination ||
		(checkpoint.ConversationCheckpointState == domain.ConversationCheckpointCoordination &&
			o.generation != "" && o.nativeID != "" &&
			checkpoint.ConversationCheckpointGeneration == o.generation &&
			checkpoint.ConversationCheckpointNativeID == o.nativeID)
	if coordinationStop {
		s.LatestAssistantUpdate = ""
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointCoordination
		checkpoint.ConversationCheckpointGeneration = o.generation
		checkpoint.ConversationCheckpointNativeID = o.nativeID
		if s.CoordinationID != "" {
			checkpoint.ConversationCheckpointTurnID = s.CoordinationID
		}
	} else if checkpoint.ConversationCheckpointState == domain.ConversationCheckpointPrompt &&
		!checkpoint.ConversationCheckpointUnsettled &&
		(s.Timestamp.IsZero() || !s.Timestamp.Before(checkpoint.LatestUserPromptAt)) &&
		o.generation != "" && o.nativeID != "" &&
		checkpoint.ConversationCheckpointGeneration == o.generation &&
		checkpoint.ConversationCheckpointNativeID == o.nativeID &&
		(checkpoint.ConversationCheckpointTurnID == "" || checkpoint.ConversationCheckpointTurnID == s.ProviderTurnID) {
		checkpoint.LatestAssistantUpdate = s.LatestAssistantUpdate
		checkpoint.LatestAssistantUpdateAt = timeOr(s.Timestamp, now)
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointComplete
	} else if o.generation != "" && o.nativeID != "" {
		// A scoped Stop without its prompt boundary proves some completed turn
		// exists, but neither text nor arrival order can identify it safely in a
		// provider replay: repeated answers collide, and a delayed prior Stop may
		// arrive after a pane-delivered prompt. Preserve the existing checkpoint
		// and retain a hard unresolved-boundary witness instead. A later canonical
		// prompt supersedes it; until then Chat replay must fail closed.
		checkpoint.ConversationCheckpointUnsettled = true
		s.LatestAssistantUpdate = ""
	} else if checkpoint.LatestUserPrompt == "" && s.LatestAssistantUpdate != "" {
		// An unowned standalone Stop can still be useful display/recovery context,
		// but it has no owner boundary that would make it a trusted history gate.
		// Retain it as legacy text without ever borrowing an earlier user prompt.
		checkpoint.LatestAssistantUpdate = s.LatestAssistantUpdate
		checkpoint.LatestAssistantUpdateAt = timeOr(s.Timestamp, now)
		checkpoint.ConversationCheckpointState = domain.ConversationCheckpointLegacy
		checkpoint.ConversationCheckpointGeneration = ""
		checkpoint.ConversationCheckpointNativeID = ""
		checkpoint.ConversationCheckpointTurnID = ""
	} else {
		// No matching pending prompt means this Stop is duplicate, delayed, or
		// missing its turn boundary. Preserve the prior coherent checkpoint.
		s.LatestAssistantUpdate = ""
	}
}
