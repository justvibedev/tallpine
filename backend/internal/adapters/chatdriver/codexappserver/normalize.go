package codexappserver

import (
	"encoding/json"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Translation from Codex app-server notifications into Tallpine's provider-neutral
// Chat events.
//
// Two rules hold here.
//
// First: method names and payload shapes come from codexproto, which is generated
// from the provider's own `generate-json-schema` output. A method literal in this
// file would be a runtime typo; a hand-written payload struct would drift from the
// provider silently. Where a struct is declared locally it is because the provider
// does NOT declare the field (see threadItem) and the reason is stated.
//
// Second: the schema says what CAN arrive, and only a live probe says what DOES.
// Every "verified" note below is a frame captured from codex-cli 0.146.0 driving a
// real account; every "never observed" note is a method this build did not emit in
// any probe, handled for forward compatibility and marked so nobody mistakes it for
// a tested path.

// Codex thread item types. Aliased to the generated discriminator so a renamed
// item type breaks the build instead of quietly matching nothing.
const (
	itemUserMessage      = codexproto.ThreadItemTypeUserMessage
	itemAgentMessage     = codexproto.ThreadItemTypeAgentMessage
	itemReasoning        = codexproto.ThreadItemTypeReasoning
	itemPlan             = codexproto.ThreadItemTypePlan
	itemCommandExecution = codexproto.ThreadItemTypeCommandExecution
	itemFileChange       = codexproto.ThreadItemTypeFileChange
	itemMcpToolCall      = codexproto.ThreadItemTypeMcpToolCall
	itemWebSearch        = codexproto.ThreadItemTypeWebSearch
	// itemContextCompaction is how a current app-server reports that it summarized
	// earlier history to reclaim context. The schema also declares a
	// `thread/compacted` notification for the same thing and marks it "Deprecated:
	// use the ContextCompaction item type instead" — and 0.146.0 emits ONLY the
	// item, never the notification. Reading the notification alone would mean Tallpine
	// silently never noticed a compaction on any current provider.
	itemContextCompaction = codexproto.ThreadItemTypeContextCompaction
)

// itemError is an item type the generated schema does NOT declare: ThreadItemType
// has no error variant in 0.146.0, and ThreadItem has no message field. It is kept
// because an older build emitted it and dropping the handling would silently lose
// a provider error, and it is spelled out here rather than in codexproto so the
// generated file stays a faithful copy of what the provider declares.
const itemError codexproto.ThreadItemType = "error"

// threadItem is codexproto.ThreadItem plus the one field the provider does not
// declare.
//
// Embedding rather than copying: the generated struct covers every field Tallpine reads,
// including the two the previous hand-written version got wrong. `tool`, not
// `toolName`, is what an mcpToolCall item actually carries — verified against a
// live call — so the old struct's ToolName never bound and every MCP call in the
// timeline was labelled "Called tool".
type threadItem struct {
	codexproto.ThreadItem
	// Message carries the text of the undeclared error item above.
	Message string `json:"message"`
}

// itemID reports the provider's item id, which the generated schema makes optional.
func (it threadItem) itemID() string { return deref(it.ID) }

// contextEnvelope reads the conversation's position in the model's context out of
// a thread/tokenUsage/updated notification.
//
// `last` and `total` are different questions and only one of them answers "how
// full is this conversation". Measured across a compaction: total stayed at 15650
// (cumulative spend, which compaction cannot undo) while last fell from 15650 to
// 4632. So last.totalTokens is the context position, and it is the only figure
// from which a reclaim can be computed.
type contextEnvelope struct {
	TokenUsage struct {
		Last struct {
			TotalTokens int64 `json:"totalTokens"`
		} `json:"last"`
		ModelContextWindow *int64 `json:"modelContextWindow"`
	} `json:"tokenUsage"`
}

// resolvedEnvelope is the params shape of serverRequest/resolved.
//
// json.RawMessage rather than codexproto.RequestID, which is a DEFINED type over
// json.RawMessage and so does not inherit its UnmarshalJSON. Decoding into it makes
// encoding/json treat the field as a byte slice and demand base64, which fails on
// the numeric ids the provider actually sends (observed: 0, 1, 2) and takes the
// whole notification down with it. A missed correlation key leaves an approval card
// actionable after the provider has already answered it, so this is read directly.
//
// `id` is tolerated alongside `requestId` for a build that renames the field.
type resolvedEnvelope struct {
	ThreadID  string          `json:"threadId"`
	RequestID json.RawMessage `json:"requestId"`
	ID        json.RawMessage `json:"id"`
}

// notificationHandlers maps each app-server notification method to the function
// that turns it into Tallpine events. A method with no entry produces none.
//
// Provider bookkeeping is deliberately absent: hook/started, hook/completed,
// remoteControl/status/changed, deprecationNotice, thread/goal/*,
// model/safetyBuffering/updated, thread/settings/updated, and anything added
// by a newer provider build. Deliberately not conversation events.
//
// Three neighbours of the handled methods are also deliberately here:
//
//   - guardianWarning restates an auto-approval decision in prose. The
//     autoApprovalReview pair in this map carries the same rationale as structure,
//     so reading both would put one decision on the timeline twice.
//   - command/exec/outputDelta and process/outputDelta belong to the
//     client-driven exec API, where the CLIENT asked the server to run
//     something. Tallpine does not use it, and an agent tool call never arrives on
//     those methods.
//   - thread/realtime/* is the voice surface. Tallpine has none, and inventing one
//     from a transcript delta would be a feature nobody asked for.
var notificationHandlers = map[string]func(notification, time.Time) []ports.ChatEvent{
	codexproto.MethodTurnStarted:                             turnStarted,
	codexproto.MethodTurnCompleted:                           turnCompleted,
	codexproto.MethodItemAgentMessageDelta:                   agentMessageDelta,
	codexproto.MethodItemReasoningSummaryTextDelta:           reasoningSummaryTextDelta,
	codexproto.MethodItemReasoningSummaryPartAdded:           reasoningSummaryPartAdded,
	codexproto.MethodItemReasoningTextDelta:                  reasoningTextDelta,
	codexproto.MethodItemCommandExecutionOutputDelta:         commandOutputDelta,
	codexproto.MethodItemCommandExecutionTerminalInteraction: terminalInteraction,
	codexproto.MethodItemMcpToolCallProgress:                 mcpToolCallProgress,
	codexproto.MethodItemFileChangeOutputDelta:               fileChangeOutputDelta,
	codexproto.MethodItemFileChangePatchUpdated:              fileChangePatchUpdated,
	codexproto.MethodItemPlanDelta:                           planDelta,
	codexproto.MethodTurnPlanUpdated:                         turnPlanUpdated,
	codexproto.MethodItemAutoApprovalReviewStarted:           autoReviewStarted,
	codexproto.MethodItemAutoApprovalReviewCompleted:         autoReviewCompleted,
	codexproto.MethodModelRerouted:                           modelRerouted,
	codexproto.MethodAccountUpdated:                          accountUpdated,
	codexproto.MethodThreadStatusChanged:                     threadStatusChanged,
	codexproto.MethodThreadArchived:                          threadArchivedChanged,
	codexproto.MethodThreadUnarchived:                        threadArchivedChanged,
	codexproto.MethodThreadClosed:                            threadClosed,
	codexproto.MethodMcpServerStartupStatusUpdated:           mcpServerStartupStatus,
	codexproto.MethodTurnDiffUpdated:                         turnDiffUpdated,
	codexproto.MethodItemStarted:                             itemStarted,
	codexproto.MethodItemCompleted:                           itemCompleted,
	codexproto.MethodThreadTokenUsageUpdated:                 threadTokenUsageUpdated,
	codexproto.MethodAccountRateLimitsUpdated:                accountRateLimitsUpdated,
	codexproto.MethodThreadCompacted:                         threadCompacted, //nolint:staticcheck // deliberately reading the deprecated spelling
	codexproto.MethodThreadNameUpdated:                       threadNameUpdated,
	codexproto.MethodServerRequestResolved:                   serverRequestResolved,
	codexproto.MethodError:                                   errorNotification,
}

// normalizeNotification converts one provider notification into zero or more
// neutral events. Returning nil means "not conversation-relevant".
//
// now is passed rather than read from the clock so the rate-limit reset instant
// can be expressed as a duration deterministically. The provider reports an
// absolute timestamp; a duration is what a reader can act on without knowing
// whether Tallpine's clock agrees with the provider's.
func normalizeNotification(n notification, now time.Time) []ports.ChatEvent {
	handler, ok := notificationHandlers[n.Method]
	if !ok {
		return nil
	}
	return handler(n, now)
}
