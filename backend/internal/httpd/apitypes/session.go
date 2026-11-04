package apitypes

import (
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// BackgroundTaskPayload is one running harness background task.
type BackgroundTaskPayload struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SessionView is the session wire shape: the domain read model plus the
// display-safe branch name and the session's attributed pull requests in the
// curated SessionPRFacts shape. One session can own many PRs (e.g. a stack), so
// prs is a list. The embedded domain.Session.Metadata and domain.Session.PRs
// fields are json:"-"; these curated fields are what serialize.
type SessionView struct {
	domain.Session
	// WaitingOn names the harness background tasks (monitors, background
	// commands) the agent is waiting on; empty when none. An idle session with
	// entries here is waiting, not stalled.
	WaitingOn []string `json:"waitingOn,omitempty"`
	// PausedUntil is set while the session's provider account is out of quota: it
	// starts no turns, report deliveries or nudges until then. Omitted otherwise.
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
	// ReauthRequired is set while the provider refuses the session's login: it
	// waits for the user to sign the harness in again, not for a reply.
	ReauthRequired bool `json:"reauthRequired,omitempty"`
	// BackgroundTasks are the running background tasks behind WaitingOn, with the
	// ids a client needs to stop one.
	BackgroundTasks []BackgroundTaskPayload `json:"backgroundTasks,omitempty"`
	Branch          string                  `json:"branch,omitempty"`
	// TerminalGeneration is an opaque renderer fence. A restarted controller
	// may deliberately reuse its terminal handle; clients must still discard the
	// old attachment when this value changes.
	TerminalGeneration string `json:"terminalGeneration,omitempty"`
	// PreviewURL is the browser preview target the desktop app opens for this
	// session, set via POST /sessions/{sessionId}/preview. Empty (omitted) when
	// no preview has been requested. Pulled from the json:"-" domain Metadata.
	PreviewURL string `json:"previewUrl,omitempty"`
	// PreviewRevision bumps on every `tallpine preview` call (even when previewUrl is
	// unchanged) so the desktop browser panel can re-navigate / refresh on a
	// repeated preview of the same target. Pulled from the json:"-" domain
	// Metadata.
	PreviewRevision int64 `json:"previewRevision,omitempty"`
	// Model is the agent model this session resolved to at spawn time. Empty
	// means the agent's default model. Pulled from the json:"-" domain Metadata.
	Model string `json:"model,omitempty"`
	// LastUserMessageAt is the latest real user-authored task direction time.
	// Lifecycle and internal automation updates do not advance it.
	LastUserMessageAt *time.Time `json:"lastUserMessageAt,omitempty"`
	// LastInteractionAt includes human direction and same-project orchestrator messages.
	LastInteractionAt *time.Time `json:"lastInteractionAt,omitempty"`
	// LastEventAt is when something a person would notice last happened: an
	// activity-state transition, a PR lifecycle or CI change, or a review
	// submission. Derived at read time; see domain.Session.LastEventAt.
	LastEventAt time.Time        `json:"lastEventAt"`
	PRs         []SessionPRFacts `json:"prs"`
	// HasArtifacts reports whether the session's output includes artifact files,
	// from its persisted output type, so list rows need no directory walk.
	HasArtifacts bool `json:"hasArtifacts,omitempty"`
	// ArtifactFiles lists the files in the session's artifact directory. Only
	// GET /sessions/{sessionId} (and the other single-session reads) fills it;
	// the session list omits it, because walking every session's directory made
	// the list O(all sessions x artifact files) per request.
	ArtifactFiles []SessionArtifactView `json:"artifactFiles,omitempty"`
}

// SessionArtifactView is one inferred file artifact for a session.
type SessionArtifactView struct {
	Path       string                     `json:"path"`
	Name       string                     `json:"name"`
	Kind       domain.SessionArtifactKind `json:"kind" enum:"html,markdown,file"`
	Size       int64                      `json:"size"`
	UpdatedAt  time.Time                  `json:"updatedAt"`
	PreviewURL string                     `json:"previewUrl,omitempty"`
	// RawURL fetches this artifact's raw bytes on the artifact preview
	// origin — a distinct host from the workspace preview origin, so a
	// workspace-relative path can never collide with an artifact-relative
	// one. Set for every kind, unlike PreviewURL (html only, meant for
	// Browser navigation rather than a raw fetch).
	RawURL string `json:"rawUrl,omitempty"`
	// InlineURL frames this page inside the chat thread, from its own origin:
	// its files load same-origin there, but the daemon refuses that origin, so
	// unlike PreviewURL the page cannot call the daemon. HTML only.
	InlineURL string `json:"inlineUrl,omitempty"`
}

// ListSessionsResponse is the body of GET /api/v1/sessions.
type ListSessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
	// NextCursor resumes a paged list (limit set) after its last session.
	// Absent on the last page.
	NextCursor string `json:"nextCursor,omitempty"`
	// Archived is present when active=true: how many terminated sessions the
	// same project and kind filters would have matched, so a client can show
	// the archive's size without downloading it.
	Archived *SessionArchiveSummary `json:"archived,omitempty"`
}

// SessionArchiveSummary sizes the archive behind an active-only session list.
type SessionArchiveSummary struct {
	Total int `json:"total"`
	// ByProject counts per project id. Standalone sessions count under "".
	ByProject map[string]int `json:"byProject"`
}

// SessionResponse is the { session } body shared by session reads and updates.
type SessionResponse struct {
	Session SessionView `json:"session"`
	// LastAssistantMessage is set only by GET /api/v1/sessions/{sessionId}: the
	// final assistant message of the session's latest main turn. Absent until a
	// turn has ended with text. It is kept out of the session list payload.
	LastAssistantMessage *SessionLastAssistantMessage `json:"lastAssistantMessage,omitempty"`
	// WorkerInstructions is set only by GET /api/v1/sessions/{sessionId}: the
	// project rules section (agentRules plus agentRulesFile, capped) the worker
	// received in its system prompt. Read-only; absent when none.
	WorkerInstructions string `json:"workerInstructions,omitempty"`
}

// SessionLastAssistantMessage is a display copy of a main turn's final
// message, from the Terminal UI stop hook or the Chat turn completion. It is
// truncated to 4 KiB and is never a replay checkpoint.
type SessionLastAssistantMessage struct {
	Text string    `json:"text" maxLength:"4096"`
	At   time.Time `json:"at"`
}
