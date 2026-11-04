package controllers

// ConversationTurnResponse is one request and the work that followed it.
type ConversationTurnResponse struct {
	ID             string `json:"id"`
	State          string `json:"state" enum:"queued,running,completed,recovered,interrupted,failed,cancelled"`
	ProviderTurnID string `json:"providerTurnId,omitempty"`
	// RetryOfTurnID is the failed source whose durable prompt created this turn.
	RetryOfTurnID string `json:"retryOfTurnId,omitempty"`
	// HasRetryAttempt remains true when the attempt is outside the active branch.
	HasRetryAttempt bool `json:"hasRetryAttempt,omitempty"`
	// HasHumanPrompt includes durable human prompts outside this timeline page.
	HasHumanPrompt bool                            `json:"hasHumanPrompt,omitempty"`
	ErrorMessage   string                          `json:"errorMessage,omitempty"`
	UsageLimit     *ConversationUsageLimitResponse `json:"usageLimit,omitempty"`
	RequestedAt    string                          `json:"requestedAt"`
	StartedAt      *string                         `json:"startedAt,omitempty"`
	CompletedAt    *string                         `json:"completedAt,omitempty"`
	// RolledBack marks a turn an undo discarded. Its messages and activities are
	// absent from this snapshot because the agent no longer remembers them; the turn
	// is still reported so a client can say what was taken back rather than letting
	// the timeline quietly shrink.
	RolledBack bool `json:"rolledBack,omitempty"`
	// Diff is what this turn has changed on disk. Absent when the provider has
	// reported nothing, which is not a claim that nothing changed: an agent
	// without diff support never reports at all.
	//
	// Carried on the snapshot the client already polls rather than behind its own
	// route. A dedicated route would be a second request, on the same cadence, for
	// data this read has already loaded -- the diff belongs to a turn, and the turn
	// list is right here. It also keeps the changed-file view and the timeline from
	// disagreeing, which two independently-timed fetches would eventually do.
	Diff *ConversationTurnDiffResponse `json:"diff,omitempty"`
	// Plan is the agent's plan for this turn, or absent when it made none. The
	// provider re-sends the whole plan on every change, so this is the current answer
	// rather than a history of one: the earlier versions are the same plan with fewer
	// steps ticked off.
	Plan *ConversationPlanResponse `json:"plan,omitempty"`
}

// ConversationUsageLimitResponse separates a quota pause from other failures.
type ConversationUsageLimitResponse struct {
	ResetsAt    *string `json:"resetsAt,omitempty"`
	AutoResume  bool    `json:"autoResume"`
	ResumeError string  `json:"resumeError,omitempty"`
}

// ConversationPlanResponse is the agent's plan for one turn.
type ConversationPlanResponse struct {
	// Explanation is the agent's note about the plan as a whole, when it gives one.
	Explanation string                         `json:"explanation,omitempty"`
	Steps       []ConversationPlanStepResponse `json:"steps"`
	// SpansTurns marks a list the agent keeps across turns. Later turns carry a
	// copy of the same list, so a client shows it once, on the latest turn.
	SpansTurns bool `json:"spansTurns,omitempty"`
}

// ConversationPlanStepResponse is one step of a plan.
//
// Structured, not prose. The per-step status is the whole point -- it is where the
// agent is up to -- and a client that wants a sentence can join the steps, while one
// that wants checkboxes cannot recover them from a sentence.
type ConversationPlanStepResponse struct {
	Text   string `json:"text"`
	Status string `json:"status" enum:"pending,in_progress,completed"`
}

// ConversationTurnDiffResponse is a turn's changed-file summary.
type ConversationTurnDiffResponse struct {
	Files []ConversationDiffFileResponse `json:"files"`
	// Truncated reports that the file list was cut at the daemon's cap, so a client
	// does not present a partial list as the whole change.
	Truncated bool `json:"truncated,omitempty"`
}

// ConversationDiffFileResponse is one changed path.
//
// No patch text. The turn view answers "what did this touch, and by how much";
// carrying every hunk would put the full diff into a body polled once a second,
// and Tallpine already has a diff surface for reading the change itself.
type ConversationDiffFileResponse struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Status    string `json:"status" enum:"added,modified,deleted,renamed"`
	// OldPath is set only for a rename.
	OldPath string `json:"oldPath,omitempty"`
	// RolledBack marks a turn an undo discarded. Its messages and activities are
	// absent from this snapshot because the agent no longer remembers them; the turn
	// is still reported so a client can say what was taken back rather than letting
	// the timeline quietly shrink.
	RolledBack bool `json:"rolledBack,omitempty"`
}

// ConversationMessageResponse is one readable block of text.
type ConversationMessageResponse struct {
	Kind              string                               `json:"kind" enum:"message"`
	ID                string                               `json:"id"`
	TurnID            string                               `json:"turnId,omitempty"`
	Sequence          int64                                `json:"sequence"`
	Revision          int64                                `json:"revision"`
	Role              string                               `json:"role" enum:"user,assistant"`
	Origin            string                               `json:"origin" enum:"human,automation,daemon,provider"`
	Text              string                               `json:"text"`
	Content           []ConversationContentSummaryResponse `json:"content,omitempty"`
	SenderSessionID   string                               `json:"senderSessionId,omitempty"`
	SenderProjectID   string                               `json:"senderProjectId,omitempty"`
	SenderDisplayName string                               `json:"senderDisplayName,omitempty"`
	// ClientMessageID echoes the sender's idempotency key so a client can match its
	// local echo to this row without comparing text or clocks.
	ClientMessageID string `json:"clientMessageId,omitempty"`
	EditAvailable   bool   `json:"editAvailable"`
	// Streaming is true while more deltas are expected for this message.
	Streaming bool `json:"streaming"`
	// Continuation is the user's one-click "continue" after stopping a turn,
	// shown as a marker instead of a message bubble.
	Continuation bool   `json:"continuation,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// ConversationActivityResponse is one non-message timeline entry.
type ConversationActivityResponse struct {
	Kind     string `json:"kind" enum:"activity"`
	ID       string `json:"id"`
	TurnID   string `json:"turnId,omitempty"`
	Sequence int64  `json:"sequence"`
	Revision int64  `json:"revision"`
	// ActivityKind discriminates the payload in Detail.
	//
	// mcp_tool is not command: an MCP call has a server, a tool name, structured
	// arguments and a structured result, and rendering it as a shell command claimed
	// the agent had run something in the worktree. auto_review is not approval: an
	// approval is a question waiting on a person, while an auto-review is a decision
	// the provider already made on their behalf, and those are opposites.
	ActivityKind string `json:"activityKind" enum:"command,file_change,plan,reasoning,approval,usage,error,system,mcp_tool,auto_review,user_input,ao_action"`
	Status       string `json:"status" enum:"running,completed,recovered,failed,cancelled,pending,resolved"`
	Summary      string `json:"summary"`
	// Detail is the provider-neutral typed payload for this kind. For an approval
	// it carries the provider's own offered decisions, which is what the client
	// renders buttons from.
	//
	// The keys that depend on the kind:
	//
	//   command      command, rawCommand, cwd, exitCode, durationMs, processId,
	//                output (+ outputSource, outputMayBePartial, outputTruncated),
	//                terminalInput -- the keystrokes the agent sent to the PTY, kept
	//                out of output because the PTY echoes them
	//   file_change  files[] with path, oldPath, status, additions, deletions, patch
	//   reasoning    text -- streamed while the model works, replaced by the
	//                provider's settled summary when the item completes
	//   mcp_tool     server, toolName, namespace, arguments, result, error, success,
	//                progress
	//   plan         event "plan", explanation, steps[] with text and status
	//   auto_review  reviewId, targetItemId, actionType, command, riskLevel,
	//                rationale, decisionSource, status, durationMs
	//   user_input   inputMode, message, schema, url, elicitationId
	//   system       event -- "compaction", "model.rerouted" or
	//                "auth.reauth_required" -- plus that event's own fields
	//   ao_action    action, operationId, sourceSessionId, targetSessionId,
	//                displayName, previousDisplayName, harness, previousHarness,
	//                href, prNumber, prTitle, error. action is the neutral
	//                outcome (session.spawned, session.terminated,
	//                session.renamed, session.restored, session.agent_switched,
	//                pull_request.claimed, pull_request.created). There is no
	//                event key: an event value is a timeline boundary, and
	//                these actions stay in the ordinary activity batch.
	Detail    map[string]any `json:"detail,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
	// ProviderItemID is the stable parent key used by nested ACP transcripts.
	ProviderItemID string `json:"providerItemId,omitempty"`
	CreatedAt      string `json:"createdAt"`
}

// ConversationSnapshotResponse is the durable read model a client bootstraps from.
type ConversationSnapshotResponse struct {
	ContinueTurnID string `json:"continueTurnId"`
	// SupportsExcerpts advertises daemon-owned source verification separately from
	// provider capabilities. Older daemons silently ignore the excerpts input.
	SupportsExcerpts           bool   `json:"supportsExcerpts,omitempty"`
	ConversationID             string `json:"conversationId"`
	ActiveBranchID             string `json:"activeBranchId,omitempty"`
	BranchedFromEarlierMessage bool   `json:"branchedFromEarlierMessage"`
	SessionID                  string `json:"sessionId"`
	Harness                    string `json:"harness,omitempty"`
	Mode                       string `json:"mode" enum:"chat,tui"`
	// Controller is reported separately from history so a client can tell "no
	// messages yet" apart from "the agent is not running".
	Controller     string `json:"controller" enum:"connecting,ready,busy,recovering,hibernated,stopped"`
	LatestSequence int64  `json:"latestSequence"`
	OldestSequence int64  `json:"oldestSequence,omitempty"`
	HasMoreBefore  bool   `json:"hasMoreBefore"`
	// NativeForkAvailableAfterSequence is the first provider-backed human prompt
	// in the active provider scope. It keeps edit gating exact across bounded pages.
	NativeForkAvailableAfterSequence int64                             `json:"nativeForkAvailableAfterSequence"`
	Turns                            []ConversationTurnResponse        `json:"turns"`
	Messages                         []ConversationMessageResponse     `json:"messages"`
	Activities                       []ConversationActivityResponse    `json:"activities"`
	BranchPoints                     []ConversationBranchPointResponse `json:"branchPoints,omitempty"`
	// BranchMaterialization says whether the selected provider branch preserved
	// native history or was rebuilt from Tallpine's bounded text transcript. Omitted for
	// conversations that have no durable branch metadata yet.
	BranchMaterialization *ConversationBranchMaterializationResponse `json:"branchMaterialization,omitempty"`
	// Settings are the provider choices for the next turn. Carried on the snapshot
	// the client already polls so the composer can label itself without a second
	// request, and so a choice made on another client shows up here.
	Settings ConversationTurnSettingsPayload `json:"settings"`
	// Title is the name the provider currently gives this thread. Empty means it has
	// not named one, which is not the same as an empty name.
	Title string `json:"title,omitempty"`
	// Usage is how full this conversation is. Omitted until the provider reports,
	// so a client can tell "not known yet" from a conversation using nothing.
	Usage *ConversationUsagePayload `json:"usage,omitempty"`
	// RateLimits is where the account stands. Omitted until the provider reports.
	RateLimits *ConversationRateLimitsPayload `json:"rateLimits,omitempty"`
	// CompactedAt is when history was last summarized to reclaim context, or absent
	// if never. On the snapshot rather than derived from the timeline so a client can
	// label the control without scanning every activity.
	CompactedAt *string `json:"compactedAt,omitempty"`
	// ModelReroute is present when the provider answered with a model other than the
	// one that was asked for. A client MUST prefer this over the selected model when
	// naming what produced the answers: without it the composer keeps advertising a
	// model that is not replying.
	ModelReroute *ConversationModelReroutePayload `json:"modelReroute,omitempty"`
	// Account is the provider account this conversation runs under. Omitted until the
	// provider says anything about it.
	Account *ConversationAccountPayload `json:"account,omitempty"`
	// ThreadState is the provider's own lifecycle view of the thread. It is NOT the
	// session's status, which stays derived from durable facts and is served on the
	// session resource; this is one more such fact.
	ThreadState *ConversationThreadStatePayload `json:"threadState,omitempty"`
	// MCPServers is the startup state of the tool servers this conversation can
	// reach. Empty means none are configured or none has reported. It answers a
	// question the timeline cannot: a tool call that never happened because its
	// server failed to start reads, from the timeline alone, as the agent choosing
	// not to use it.
	MCPServers []ConversationMCPServerPayload `json:"mcpServers,omitempty"`
	// Capabilities names what this session's provider can do, so a client gates a
	// control before drawing it. Sorted, and only the abilities the provider
	// actually has are listed.
	//
	// An open list rather than a fixed set of booleans: drivers gain abilities, and
	// a client that checks for membership keeps working against a daemon that knows
	// about more of them than it does. Absent until a controller is live, because an
	// unstarted session's abilities are not yet known — and a client must treat
	// absent as "do not offer yet" rather than as "cannot".
	Capabilities []string `json:"capabilities,omitempty"`
}

// ConversationBranchMaterializationResponse describes the fidelity of the
// active branch's provider context without exposing provider-owned identifiers.
type ConversationBranchMaterializationResponse struct {
	Strategy        string `json:"strategy" enum:"native,approximate_context"`
	ReplayTruncated bool   `json:"replayTruncated"`
}

// ConversationBranchPointResponse describes sibling continuations at one prompt.
type ConversationBranchPointResponse struct {
	TurnID           string `json:"turnId"`
	Position         int    `json:"position"`
	Total            int    `json:"total"`
	PreviousBranchID string `json:"previousBranchId,omitempty"`
	NextBranchID     string `json:"nextBranchId,omitempty"`
}

// ConversationModelReroutePayload is the provider answering with a model other than
// the one that was asked for.
type ConversationModelReroutePayload struct {
	FromModel string `json:"fromModel,omitempty"`
	ToModel   string `json:"toModel"`
	// Reason is the provider's own word for why, carried verbatim rather than
	// translated: Tallpine cannot improve on the provider's account of its own policy.
	Reason string `json:"reason,omitempty"`
	// ProviderTurnID is the turn it happened on, so a client can point at the
	// exchange rather than only at the conversation.
	ProviderTurnID string `json:"providerTurnId,omitempty"`
	At             string `json:"at"`
}

// ConversationAccountPayload is what the provider says about the account behind a
// conversation.
type ConversationAccountPayload struct {
	AuthenticationState   string  `json:"authenticationState,omitempty" enum:"unknown,required,authenticated"`
	AuthVerifiedAt        *string `json:"authVerifiedAt,omitempty"`
	LastAuthFailureAt     *string `json:"lastAuthFailureAt,omitempty"`
	LastAuthFailureReason string  `json:"lastAuthFailureReason,omitempty"`
	AuthFailureID         string  `json:"authFailureId,omitempty"`
	AuthMode              string  `json:"authMode,omitempty"`
	PlanLabel             string  `json:"planLabel,omitempty"`
	// ReauthRequiredAt is when the provider last asked for credentials the daemon
	// does not hold. Present means a demand has not yet been superseded by verified success.
	ReauthRequiredAt *string `json:"reauthRequiredAt,omitempty"`
	ReauthReason     string  `json:"reauthReason,omitempty"`
}

// ConversationThreadStatePayload is the provider's lifecycle view of the thread.
type ConversationThreadStatePayload struct {
	Status string `json:"status,omitempty" enum:"active,idle,not_loaded,system_error,closed"`
	// WaitingOn are the provider's active flags. A thread can be active AND blocked
	// on a person, and those are different states.
	WaitingOn []string `json:"waitingOn,omitempty"`
	// ArchivedAt is present while the provider considers the thread archived.
	// Archiving is reversible, so this returns to absent on unarchive.
	ArchivedAt *string `json:"archivedAt,omitempty"`
	// ClosedAt is when the provider dropped the thread. Recorded rather than acted
	// on: the daemon has never observed this, so tearing a controller down on the
	// strength of it would be a guess.
	ClosedAt *string `json:"closedAt,omitempty"`
	// Compaction is the latest context compaction, absent until one is reported.
	Compaction *ConversationCompactionPayload `json:"compaction,omitempty"`
}

// ConversationCompactionPayload is the latest context compaction.
type ConversationCompactionPayload struct {
	Status string `json:"status" enum:"compacting,completed,failed"`
	// Trigger is "auto" or "manual", absent when the provider did not say.
	Trigger string `json:"trigger,omitempty"`
	// PreTokens and PostTokens are absent when the provider did not report them.
	PreTokens  int64  `json:"preTokens,omitempty"`
	PostTokens int64  `json:"postTokens,omitempty"`
	At         string `json:"at"`
}

// ConversationMCPServerPayload is one tool server's startup state.
type ConversationMCPServerPayload struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// Error is the provider's failure text; FailureReason is its classification,
	// which is actionable in a way a message is not.
	Error         string `json:"error,omitempty"`
	FailureReason string `json:"failureReason,omitempty"`
}

// ReloadConversationMCPServersResponse reports the tool servers after a reload.
//
// The list is what the provider reported when asked. Its own startup notifications
// remain the authoritative account and land on the conversation regardless, so a
// client that polls the snapshot will converge on the same answer.
type ReloadConversationMCPServersResponse struct {
	Servers []ConversationMCPServerPayload `json:"servers"`
}

// ConversationUsagePayload is the conversation's token position.
//
// Current state on the snapshot rather than timeline entries: the provider reports
// this after every tool call, and one row per report is what buried the
// conversation before.
type ConversationUsagePayload struct {
	// ContextUsed and ContextWindow are what let a client draw a meter instead of
	// printing a bare number. ContextWindow is 0 when the provider would not state
	// one, and a client must then show the tokens without a fullness claim.
	ContextUsed   int64 `json:"contextUsed"`
	ContextWindow int64 `json:"contextWindow"`
	// The conversation's cumulative spend, which is a different question from
	// fullness: it grows without bound while context rises and falls.
	InputTokens  int64    `json:"inputTokens"`
	OutputTokens int64    `json:"outputTokens"`
	CachedTokens int64    `json:"cachedTokens"`
	TotalTokens  int64    `json:"totalTokens"`
	Cost         *float64 `json:"cost,omitempty"`
	Currency     string   `json:"currency,omitempty"`
}

// ConversationRateLimitsPayload is the account's quota position, which is why a
// turn can fail for reasons that have nothing to do with the request.
type ConversationRateLimitsPayload struct {
	// Percentages in 0..100. Negative means the provider did not report that
	// window, which is not the same as reporting it empty.
	PrimaryUsedPercent   float64 `json:"primaryUsedPercent"`
	SecondaryUsedPercent float64 `json:"secondaryUsedPercent"`
	// Seconds remaining, not the absolute reset instant: a duration cannot read as
	// already-refilled once the snapshot is a few minutes old.
	PrimaryResetsInSeconds   int64  `json:"primaryResetsInSeconds,omitempty"`
	SecondaryResetsInSeconds int64  `json:"secondaryResetsInSeconds,omitempty"`
	PlanLabel                string `json:"planLabel,omitempty"`
	// Title is the name the provider currently gives this thread. Empty means it has
	// none, which is the normal state until something names it.
	Title string `json:"title,omitempty"`
}

// ConversationRequestIDParam is the provider's approval request id. Resolving
// matches on it, so a card left on screen cannot answer a newer request.
type ConversationRequestIDParam struct {
	RequestID string `path:"requestId" description:"Provider approval request identifier. Zero is a legitimate value."`
}

// ConversationConfigIDParam names one provider-advertised session option.
type ConversationConfigIDParam struct {
	ConfigID string `path:"configId" description:"Provider session configuration option identifier."`
}

// ConversationTurnIDParam names one turn in a session's conversation.
type ConversationTurnIDParam struct {
	TurnID string `path:"turnId" description:"Tallpine conversation turn identifier, from the snapshot's turns array."`
}
