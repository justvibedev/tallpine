package controllers

import (
	"encoding/json"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// ConversationPromptSuggestionRequest identifies the latest completed turn.
type ConversationPromptSuggestionRequest struct {
	TurnID string `json:"turnId"`
}

// ConversationPromptSuggestionResponse offers one optional next human message.
type ConversationPromptSuggestionResponse struct {
	TurnID string `json:"turnId"`
	// Empty when there is no useful suggestion.
	Prompt string `json:"prompt"`
}

// SetChatViewRequest renews or releases one renderer's Chat view lease.
type SetChatViewRequest struct {
	ViewID        string `json:"viewId"`
	Active        bool   `json:"active"`
	activePresent bool
}

// UnmarshalJSON distinguishes an omitted active value from an explicit false
// while keeping the generated API schema non-nullable.
func (r *SetChatViewRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		ViewID string `json:"viewId"`
		Active *bool  `json:"active"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	r.ViewID = wire.ViewID
	r.Active = wire.Active != nil && *wire.Active
	r.activePresent = wire.Active != nil
	return nil
}

// SendConversationMessageRequest is a message for a Chat session's agent.
type SendConversationMessageRequest struct {
	Text string `json:"text"`
	// ClientMessageID makes delivery idempotent. A retry carrying the same value
	// must not produce a second provider turn.
	ClientMessageID string                                `json:"clientMessageId,omitempty"`
	Attachments     []ConversationImageContentRequest     `json:"attachments,omitempty"`
	Resources       []ConversationResourceContentRequest  `json:"resources,omitempty"`
	Excerpts        []ConversationExcerptReferenceRequest `json:"excerpts,omitempty"`
	// Continuation marks the user's request to continue an interrupted task.
	Continuation bool `json:"continuation,omitempty"`
}

// ConversationExcerptReferenceRequest attaches verified selected transcript
// text to the next message.
type ConversationExcerptReferenceRequest struct {
	ConversationID string `json:"conversationId"`
	MessageID      string `json:"messageId"`
	Revision       int64  `json:"revision"`
	Text           string `json:"text"`
}

// ProviderSwitchRequest switches an idle session to another provider. See
// domain.ProviderSwitchRequest for what each field starts the target with.
type ProviderSwitchRequest struct {
	TargetHarness     string `json:"targetHarness"`
	IdempotencyKey    string `json:"idempotencyKey,omitempty"`
	Model             string `json:"model,omitempty" maxLength:"200"`
	Effort            string `json:"effort,omitempty" maxLength:"64"`
	FastMode          bool   `json:"fastMode,omitempty"`
	Ultracode         bool   `json:"ultracode,omitempty"`
	AutoCompactWindow string `json:"autoCompactWindow,omitempty" description:"Empty (Auto), a size from 50k to 1m (Claude Code runs anything under 100k as 100k), or a whole percent from 20% to 95% of the model window."`
	// PermissionMode is the mode picked for the new agent; empty keeps the chat's.
	PermissionMode string `json:"permissionMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	// Message is sent to the new agent as its first turn once the switch
	// completes, in the same request. Attachments are sent separately.
	Message *SendConversationMessageRequest `json:"message,omitempty"`
}

// ProviderSwitchResponse reports a completed switch, and the send of a message
// that came with it.
type ProviderSwitchResponse struct {
	Status  string                           `json:"status"`
	Message *SendConversationMessageResponse `json:"message,omitempty"`
}

// SessionHistoryQuery pages GET /api/v1/sessions/{sessionId}/history.
type SessionHistoryQuery struct {
	Offset int `query:"offset,omitempty" minimum:"0" description:"Number of newest turns to skip."`
	Limit  int `query:"limit,omitempty" minimum:"1" maximum:"50" description:"Maximum turns to return. Defaults to 10."`
}

// EditQueuedConversationMessageRequest changes an undispatched prompt. Omitted
// retainedContent preserves the stored blocks; an empty list removes attachments.
type EditQueuedConversationMessageRequest struct {
	// Stable retry key for this exact edit, including its attachments.
	ClientMessageID string                            `json:"clientMessageId,omitempty"`
	Text            string                            `json:"text"`
	Attachments     []ConversationImageContentRequest `json:"attachments,omitempty"`
	// Indices in the message's public content summary, in their original order.
	RetainedContent *[]int `json:"retainedContent,omitempty"`
	// Reject a stale editor before interpreting attachment indices.
	ExpectedRevision *int64 `json:"expectedRevision,omitempty"`
}

// ConversationResourceContentRequest is a resource link, or embedded text when
// Text is present and the provider negotiated embedded context.
type ConversationResourceContentRequest struct {
	URI      string  `json:"uri"`
	Name     string  `json:"name"`
	MIMEType string  `json:"mimeType,omitempty"`
	Text     *string `json:"text,omitempty"`
}

// SendConversationMessageResponse reports what the send did.
type SendConversationMessageResponse struct {
	TurnID         string `json:"turnId,omitempty"`
	ProviderTurnID string `json:"providerTurnId,omitempty"`
	// State is `running` when the agent picked the message up immediately and
	// `queued` when it arrived mid-turn and will be sent once the turn ends. A
	// client that only reads turnId cannot tell those apart, and "accepted" is not
	// the same claim as "delivered".
	State domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
	// Duplicate is true when this client message id was already delivered, so a
	// retrying client can stop instead of assuming a new turn began.
	Duplicate bool `json:"duplicate"`
	// Cleared is true when the message was a /clear that reset the agent's
	// context: no turn was created, and activityId is the transcript marker.
	Cleared    bool   `json:"cleared,omitempty"`
	ActivityID string `json:"activityId,omitempty"`
}

// SteerConversationResponse reports the turn the guidance joined.
type SteerConversationResponse struct {
	// ProviderTurnID is the turn that absorbed it. Against Codex this is the turn
	// that was already running — steering does not open a new one — so a client
	// matches it against the turn it is already rendering.
	ProviderTurnID string `json:"providerTurnId"`
	// ActivityID is the timeline row recording the guidance, so an optimistic bubble
	// can be reconciled with the durable one rather than shown twice.
	ActivityID string `json:"activityId,omitempty"`
}

// EditConversationMessageRequest changes the readable text of one durable human
// prompt. Structured content is intentionally absent: the service reuses the
// server-side blocks recorded with the original message.
type EditConversationMessageRequest struct {
	Text            string `json:"text"`
	ClientMessageID string `json:"clientMessageId,omitempty"`
}

// ConversationContentSummaryResponse is a lightweight attachment/resource chip.
// Image bytes and embedded resource text never leave the durable server record.
type ConversationContentSummaryResponse struct {
	Type     string `json:"type"`
	MIMEType string `json:"mimeType,omitempty"`
	URI      string `json:"uri,omitempty"`
	Name     string `json:"name,omitempty"`
	// Text is exposed only for verified chat excerpts, so the timeline can show
	// what the user referred to without exposing internal resource URIs.
	Text string `json:"text,omitempty"`
	// SourceMessageID and SourceRevision let the renderer navigate back to the
	// verified transcript message without exposing the internal excerpt URI.
	SourceConversationID string `json:"sourceConversationId,omitempty"`
	SourceMessageID      string `json:"sourceMessageId,omitempty"`
	SourceRevision       int64  `json:"sourceRevision,omitempty"`
}

// EditConversationMessageResponse identifies the newly selected branch and its
// replacement turn.
type EditConversationMessageResponse struct {
	SourceBranchID string           `json:"sourceBranchId"`
	ActiveBranchID string           `json:"activeBranchId"`
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
}

// RetryTurnResponse reports the new turn a retry dispatched. The original failed
// turn is not referenced here: it stays failed and unchanged, and both attempts
// remain separately visible in history.
type RetryTurnResponse struct {
	TurnID         string           `json:"turnId,omitempty"`
	ProviderTurnID string           `json:"providerTurnId,omitempty"`
	State          domain.TurnState `json:"state,omitempty" enum:"queued,running,completed,recovered,interrupted,failed"`
}

// SetTurnAutoResumeRequest opts one usage-limited turn into a scheduled retry.
type SetTurnAutoResumeRequest struct {
	Enabled *bool `json:"enabled"`
}

// ActivateConversationBranchResponse reports the durable head after switching.
type ActivateConversationBranchResponse struct {
	ActiveBranchID string `json:"activeBranchId"`
}

// ConversationModelsResponse is the provider's model catalog plus what is selected.
type ConversationModelsResponse struct {
	Models   []ConversationModelResponse     `json:"models"`
	Selected ConversationTurnSettingsPayload `json:"selected"`
}

// ConversationConfigOptionsResponse is the provider's complete live session
// configuration catalog. Clients replace their cached list after a mutation:
// model changes can add or remove dependent controls.
type ConversationConfigOptionsResponse struct {
	Options []ConversationConfigOptionResponse `json:"options"`
}

// ConversationConfigOptionResponse is one provider-advertised session control.
type ConversationConfigOptionResponse struct {
	ID             string                             `json:"id"`
	Name           string                             `json:"name"`
	Description    string                             `json:"description,omitempty"`
	Category       string                             `json:"category,omitempty"`
	Type           string                             `json:"type" enum:"select,boolean"`
	CurrentValue   string                             `json:"currentValue,omitempty"`
	CurrentBoolean *bool                              `json:"currentBoolean,omitempty"`
	Choices        []ConversationConfigChoiceResponse `json:"choices,omitempty"`
}

// ConversationConfigChoiceResponse is one value in a provider select.
type ConversationConfigChoiceResponse struct {
	PermissionMode domain.PermissionMode `json:"permissionMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	Value          string                `json:"value"`
	Name           string                `json:"name"`
	Description    string                `json:"description,omitempty"`
	Group          string                `json:"group,omitempty"`
	GroupName      string                `json:"groupName,omitempty"`
}

// SetConversationConfigOptionRequest selects one provider-advertised value.
// Selects use Value; booleans use Enabled. Exactly one must be present.
type SetConversationConfigOptionRequest struct {
	Value   string `json:"value,omitempty"`
	Enabled *bool  `json:"enabled,omitempty"`
}

// ConversationModelResponse is one model the provider offers.
type ConversationModelResponse struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	// Default marks the model the provider would pick on its own, so a client can
	// label it rather than inventing its own idea of a default.
	Default bool `json:"default"`
	// Efforts are the reasoning levels this model supports, in the provider's
	// order. Empty means the model does not take one.
	Efforts       []string `json:"efforts,omitempty"`
	DefaultEffort string   `json:"defaultEffort,omitempty"`
	// SupportsFast reports that the model can run on the provider's faster tier.
	SupportsFast bool `json:"supportsFast,omitempty"`
}

// ConversationSkillsResponse is the named skills the provider will let this
// session invoke.
//
// An empty list is a real answer, not a failure: it means this agent offers no
// skills, and a client must render that as "no commands" rather than as an error.
type ConversationSkillsResponse struct {
	Skills []ConversationSkillResponse `json:"skills"`
}

// ConversationSkillResponse is one skill a user can invoke by name.
type ConversationSkillResponse struct {
	// Name is the invocable identifier. It is what a client puts in the message
	// text; DisplayName is only a label.
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	// InputHint is the provider's short placeholder for command arguments.
	InputHint string `json:"inputHint,omitempty"`
	// Source is where the skill came from (the provider's scope: user, repo,
	// system, admin), so a user can tell a repo skill from one of their own.
	Source string `json:"source,omitempty"`
}

// ConversationTurnSettingsPayload is the provider choices for the next turn. It is
// both the request body for changing them and the echo of what is now stored.
//
// Every field is optional and an empty value means "use the provider's default",
// so clearing a choice and never making one are the same thing.
type ConversationTurnSettingsPayload struct {
	Model           string `json:"model,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
	ApprovalMode    string `json:"approvalMode,omitempty" enum:"default,accept-edits,auto,bypass-permissions"`
	// The rest are described on domain.ConversationSettings.
	FastMode          bool   `json:"fastMode,omitempty"`
	PlanMode          bool   `json:"planMode,omitempty"`
	AutoCompactWindow string `json:"autoCompactWindow,omitempty" description:"Empty (Auto), a size from 50k to 1m (Claude Code runs anything under 100k as 100k), or a whole percent from 20% to 95% of the model window."`
	// AutoCompactPending is set by the daemon, never read from a request: the
	// running agent still compacts at an older value, which applies after a restart.
	AutoCompactPending bool `json:"autoCompactPending,omitempty"`
	// AutoCompactProjectTokens is set by the daemon, never read from a request:
	// the window the project's own environment sets, which Auto leaves in force.
	AutoCompactProjectTokens int `json:"autoCompactProjectTokens,omitempty"`
	// ContextCapacity, CompactThreshold and ModelLabel are set by the daemon,
	// never read from a request: the model's real window, where the chosen
	// auto-compact setting compacts against it, and the catalog's model name.
	ContextCapacity  int64  `json:"contextCapacity,omitempty"`
	CompactThreshold int    `json:"compactThreshold,omitempty"`
	ModelLabel       string `json:"modelLabel,omitempty"`
}

// ResolveConversationApprovalRequest answers a pending approval. DecisionID must
// be one the provider offered for that request; Tallpine does not invent options.
type ResolveConversationApprovalRequest struct {
	DecisionID string `json:"decisionId"`
	// Note is a short reason recorded in the audit trail; only an orchestrator's
	// answer keeps it.
	Note string `json:"note,omitempty"`
}

// ResolveConversationInputRequest answers a structured form or URL-consent
// request. Content is meaningful only for accept.
type ResolveConversationInputRequest struct {
	Action  string         `json:"action" enum:"accept,decline,cancel"`
	Content map[string]any `json:"content,omitempty"`
}

// CompactConversationResponse reports a compaction the provider accepted.
//
// Accepted, not finished. The provider takes the request and does the work as its
// own turn over the following seconds, so this says what is about to be reclaimed
// and the settled figures arrive on the timeline as a compaction entry. A client
// that wants the outcome reads the timeline, which is where it belongs anyway:
// the reclaim is durable history, not the answer to one request.
type CompactConversationResponse struct {
	// TokensBefore is the conversation's context position when compaction was
	// requested. Zero means the provider has not reported one yet, in which case
	// Tallpine deliberately claims no figure rather than guessing at one.
	TokensBefore int64 `json:"tokensBefore,omitempty"`
	// TokensAfter is only set by a provider that compacts synchronously. Zero means
	// the reclaim is still in flight.
	TokensAfter int64 `json:"tokensAfter,omitempty"`
}

// RenderIDParam names a published render.
type RenderIDParam struct {
	RenderID string `path:"renderId" description:"Render identifier returned when the page was published."`
}

// ConversationBranchIDParam names one durable provider-thread branch.
type ConversationBranchIDParam struct {
	BranchID string `path:"branchId" description:"Conversation branch identifier, from a snapshot branch navigation point."`
}

// RollbackConversationResponse reports what an undo discarded.
type RollbackConversationResponse struct {
	// TurnsDiscarded counts the turns the agent no longer remembers, including the
	// one the caller named. A client can say how much was taken back instead of
	// leaving the user to notice the timeline is shorter.
	TurnsDiscarded int `json:"turnsDiscarded"`
}

// SetConversationTitleRequest names the provider's thread.
type SetConversationTitleRequest struct {
	Title string `json:"title"`
}

// SetConversationTitleResponse echoes the normalized title.
//
// Accepted rather than applied: the provider confirms the name and then reports it
// back on its own event, and that report is what updates Tallpine's rows. So this is the
// title Tallpine asked for, which is not yet proof the session label has moved.
type SetConversationTitleResponse struct {
	Title string `json:"title"`
}

/* ---- settings ---------------------------------------------------------- */
