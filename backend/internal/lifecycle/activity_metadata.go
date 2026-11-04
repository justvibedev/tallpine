package lifecycle

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// needsInputResolutions reports the needs-input notification a session write
// just made stale. A session that stopped waiting on the user — because the
// input arrived, or because the session ended — has nothing left to resolve.
func needsInputResolutions(prev, next domain.SessionRecord, now time.Time) []ports.NotificationResolution {
	if !prev.Activity.State.NeedsInput() {
		return nil
	}
	if next.Activity.State.NeedsInput() && !next.IsTerminated {
		return nil
	}
	return []ports.NotificationResolution{{
		Type:       domain.NotificationNeedsInput,
		SessionID:  next.ID,
		ResolvedAt: now,
	}}
}

// sameActivity reports whether two activity signals describe the same state.
// LastActivityAt is intentionally ignored: same-state repeats (e.g. a stream
// of idle notifications) must not rewrite UpdatedAt or fan out a CDC event.
// LastActivityAt now marks when this state was first entered since the last
// transition, which is the timestamp a UI actually wants.
func sameActivity(a, b domain.Activity) bool {
	return a.State == b.State
}

func mergeMetadata(base, in domain.SessionMetadata) domain.SessionMetadata {
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&base.Branch, in.Branch)
	set(&base.WorkspacePath, in.WorkspacePath)
	set(&base.WorkspaceRepoPath, in.WorkspaceRepoPath)
	set(&base.DiffBaseSHA, in.DiffBaseSHA)
	set(&base.DiffBaseRef, in.DiffBaseRef)
	set(&base.RuntimeHandleID, in.RuntimeHandleID)
	base.RuntimeLaunchID = in.RuntimeLaunchID
	set(&base.AgentSessionID, in.AgentSessionID)
	set(&base.AgentSessionIDLaunchID, in.AgentSessionIDLaunchID)
	set(&base.Prompt, in.Prompt)
	set(&base.LatestUserPrompt, in.LatestUserPrompt)
	if !in.LatestUserPromptAt.IsZero() {
		base.LatestUserPromptAt = in.LatestUserPromptAt
	}
	if !in.LatestAssistantUpdateAt.IsZero() {
		base.LatestAssistantUpdateAt = in.LatestAssistantUpdateAt
	}
	if !in.NativeIdentityObservedAt.IsZero() {
		base.NativeIdentityObservedAt = in.NativeIdentityObservedAt
	}
	set(&base.LatestAssistantUpdate, in.LatestAssistantUpdate)
	set(&base.NativeTranscriptPath, in.NativeTranscriptPath)
	set(&base.Model, in.Model)
	set(&base.Effort, in.Effort)
	set(&base.BrowserCapabilityVerifier, in.BrowserCapabilityVerifier)
	// The chat controller's resume handle. Without this a restart has no thread to
	// resume and the conversation is stranded — the provider still holds it, but
	// Tallpine no longer knows its id.
	set(&base.ProviderConversationID, in.ProviderConversationID)
	// Assigned rather than set: a relaunch rotates the generation, and the whole
	// point is that the new value replaces the old one so events from the
	// controller this one superseded can be told apart.
	base.ControllerGeneration = in.ControllerGeneration
	return base
}

func applyActivityMetadata(meta *domain.SessionMetadata, signal ports.ActivitySignal) {
	if signal.AgentSessionID != "" {
		meta.AgentSessionID = signal.AgentSessionID
		meta.AgentSessionIDLaunchID = signal.LaunchID
		if signal.Timestamp.After(meta.NativeIdentityObservedAt) {
			meta.NativeIdentityObservedAt = signal.Timestamp
		}
	}
	if signal.TranscriptPath != "" {
		meta.NativeTranscriptPath = signal.TranscriptPath
	}
}
