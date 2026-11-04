package codexappserver

import (
	"cmp"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/adapters/chatdriver/codexappserver/codexproto"
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// deltaEvent builds the event for the streamed-text notifications. Each handler
// decodes its own generated codexproto type, so a schema change breaks the
// build; only the event construction is shared. An empty delta is no event.
func deltaEvent(kind ports.ChatEventKind, turnID, itemID, delta string) []ports.ChatEvent {
	if delta == "" {
		return nil
	}
	return []ports.ChatEvent{{Kind: kind, ProviderTurnID: turnID, ProviderItemID: itemID, Delta: delta}}
}

func turnStarted(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.TurnStartedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:                   ports.ChatEventTurnStarted,
		ProviderTurnID:         cmp.Or(p.Turn.ID, turnIDFallback(n.Params)),
		ProviderConversationID: p.ThreadID,
	}}
}

func turnCompleted(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.TurnCompletedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	ev := ports.ChatEvent{
		Kind:                   ports.ChatEventTurnCompleted,
		ProviderTurnID:         cmp.Or(p.Turn.ID, turnIDFallback(n.Params)),
		ProviderConversationID: p.ThreadID,
		TurnState:              turnStateFrom(string(p.Turn.Status)),
	}
	if p.Turn.Error != nil {
		ev.Err = codexProviderFailure(*p.Turn.Error)
	}
	return []ports.ChatEvent{ev}
}

func agentMessageDelta(n notification, _ time.Time) []ports.ChatEvent {
	var p codexproto.AgentMessageDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventMessageDelta, p.TurnID, p.ItemID, p.Delta)
}

func reasoningSummaryTextDelta(n notification, _ time.Time) []ports.ChatEvent {
	// Streamed reasoning summary. VERIFIED live: a turn on gpt-5.6-sol with
	// model_reasoning_summary=detailed produced summaryPartAdded followed by
	// summaryTextDelta for each reasoning item, and the same text arrived again in
	// the item's `summary` array on item/completed.
	//
	// Without this, reasoning only existed as the completed item — and the
	// previous build did not read `summary` either, so a reasoning row carried no
	// text at all, ever. Streaming it is what makes "thinking" visible while it
	// happens rather than as an empty placeholder that never fills in.
	var p codexproto.ReasoningSummaryTextDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventReasoningDelta, p.TurnID, p.ItemID, p.Delta)
}

func reasoningSummaryPartAdded(n notification, _ time.Time) []ports.ChatEvent {
	// A new summary section starts. VERIFIED live, always immediately before the
	// first delta of each part.
	//
	// It becomes a separator rather than an event of its own: the parts are
	// consecutive chunks of one reasoning stream, and concatenating them without a
	// break would run the end of one thought into the start of the next. Part 0
	// needs no separator, which is why the index is what decides — a stateless
	// rule, so a reconnect that misses part 0 cannot leave the stream permanently
	// mis-joined.
	var p codexproto.ReasoningSummaryPartAddedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || p.SummaryIndex <= 0 {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventReasoningDelta,
		ProviderTurnID: p.TurnID,
		ProviderItemID: p.ItemID,
		Delta:          "\n\n",
	}}
}

func reasoningTextDelta(n notification, _ time.Time) []ports.ChatEvent {
	// Raw reasoning, as opposed to the summary above. NEVER OBSERVED: probed with
	// show_raw_agent_reasoning=true on a ChatGPT-authed account and the provider
	// emitted only summaries. Handled because a build or model that does expose
	// raw reasoning would otherwise show an empty reasoning row.
	//
	// It folds into the same accumulation as the summary. A build emitting both
	// would interleave them in the provider's own order, which is the best account
	// available: Tallpine cannot know which stream a reader wanted, and dropping one
	// would silently discard reasoning the provider chose to send.
	var p codexproto.ReasoningTextDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventReasoningDelta, p.TurnID, p.ItemID, p.Delta)
}

func commandOutputDelta(n notification, _ time.Time) []ports.ChatEvent {
	// Streamed stdout/stderr from a running command. Two things make this worth
	// accumulating rather than waiting for aggregatedOutput on item/completed:
	// the aggregate does not exist until the command ends, so a long command
	// shows nothing while it runs; and a commandExecution that never completes
	// (three starts producing one completion has been observed) has no
	// aggregate at all, only these.
	//
	// It does NOT make the record complete. Measured on codex-cli 0.146.0: a
	// command printing tick-1..tick-8 one per second produced 7 deltas starting
	// at tick-2, and an aggregate of exactly those same 7 lines. The first
	// chunk is lost upstream of both channels, so a reader still has to say the
	// output may be partial.
	var p codexproto.CommandExecutionOutputDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventCommandOutputDelta, p.TurnID, p.ItemID, p.Delta)
}

func terminalInteraction(n notification, _ time.Time) []ports.ChatEvent {
	// The agent typed into a running command's terminal. VERIFIED live: asking for
	// a persistent python REPL produced one of these carrying stdin
	// "print(6*7)\n" against the PTY process id of the commandExecution item.
	//
	// Reported as its own kind, not as output. The PTY echoes what is typed —
	// measured, as ANSI insert-character sequences in the output stream — so
	// appending it to the output would duplicate it. Without this, a transcript
	// shows a REPL answering questions nobody can see being asked.
	var p codexproto.TerminalInteractionNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || p.Stdin == "" {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventCommandInput,
		ProviderTurnID: p.TurnID,
		ProviderItemID: p.ItemID,
		Delta:          p.Stdin,
		// The PTY the keystrokes went to, so a reader can tell two concurrent
		// sessions apart rather than assuming one terminal per turn.
		Detail: encodeDetail(map[string]any{"processId": p.ProcessID}),
	}}
}

func mcpToolCallProgress(n notification, _ time.Time) []ports.ChatEvent {
	// Progress from a long MCP tool call. NEVER OBSERVED: a probe MCP server that
	// sent notifications/progress with the progressToken Codex supplied produced
	// no such notification from app-server, so 0.146.0 does not appear to forward
	// MCP progress. Handled because the cost is a few lines and the alternative,
	// for a build that does forward it, is a tool call that sits silent for
	// minutes.
	var p codexproto.McpToolCallProgressNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || p.Message == "" {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventActivityText,
		ProviderTurnID: p.TurnID,
		ProviderItemID: p.ItemID,
		Delta:          p.Message + "\n",
	}}
}

func fileChangeOutputDelta(n notification, _ time.Time) []ports.ChatEvent {
	// Textual output from apply_patch. The generated schema documents this as
	// "Deprecated legacy notification ... The server no longer emits this
	// notification", and no probe saw one. Handled so a provider that still does
	// is not silently dropped; not a tested path.
	var p codexproto.FileChangeOutputDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventActivityText, p.TurnID, p.ItemID, p.Delta)
}

func fileChangePatchUpdated(n notification, _ time.Time) []ports.ChatEvent {
	// One file-change item's patch, restated. NEVER OBSERVED across four probes
	// including a single 250-line patch and a three-file patch: 0.146.0 sends the
	// complete `changes` array on item/started instead, immediately followed by
	// item/completed.
	//
	// Handled anyway, and deliberately as an update to the SAME activity the item
	// created: a build that streams a patch would otherwise leave the row showing
	// whatever the first frame contained. It does not compete with
	// turn/diff/updated, which answers a different question (what the whole turn
	// changed, aggregated) and lands on the turn rather than on an item.
	var p codexproto.FileChangePatchUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || len(p.Changes) == 0 {
		return nil
	}
	files := fileChanges(p.Changes)
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventActivityStarted,
		ProviderTurnID: p.TurnID,
		ProviderItemID: p.ItemID,
		ActivityKind:   domain.ActivityKindFileChange,
		ActivityStatus: domain.ActivityStatusRunning,
		Summary:        fileChangeSummary(files),
		Detail:         encodeDetail(map[string]any{"files": files}),
	}}
}

func planDelta(n notification, _ time.Time) []ports.ChatEvent {
	// Streaming of the plan tool's own text. NEVER OBSERVED: in a probe where the
	// agent used update_plan three times, the plan arrived only as
	// turn/plan/updated and no `plan` item was ever created. Handled as an
	// activity text stream rather than as a plan, because a partial plan is text
	// and parsing half of one into steps would invent structure.
	var p codexproto.PlanDeltaNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return deltaEvent(ports.ChatEventActivityText, p.TurnID, p.ItemID, p.Delta)
}

func turnPlanUpdated(n notification, _ time.Time) []ports.ChatEvent {
	// The agent's plan for the turn. VERIFIED live: three updates in one turn,
	// each carrying the WHOLE step list with per-step status, which is why this is
	// turn state a projection overwrites rather than a timeline row per update.
	var p codexproto.TurnPlanUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	plan := planFrom(p)
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventPlanUpdated,
		ProviderTurnID: p.TurnID,
		Plan:           &plan,
		Summary:        planSummary(plan),
	}}
}

func modelRerouted(n notification, _ time.Time) []ports.ChatEvent {
	// The provider answered with a model other than the one asked for. NEVER
	// OBSERVED: the only reason the schema declares is highRiskCyberActivity, and
	// there is no honest way to provoke that from a test.
	//
	// Handled because of the failure it prevents rather than the frequency it
	// happens at: the composer names the model it is sending to, so an unrecorded
	// substitution means every later reading of that turn attributes the answer to
	// a model that did not produce it.
	var p codexproto.ModelReroutedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil || p.ToModel == "" {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventModelRerouted,
		ProviderTurnID: p.TurnID,
		Reroute: &ports.ChatModelReroute{
			FromModel: p.FromModel,
			ToModel:   p.ToModel,
			Reason:    string(p.Reason),
		},
	}}
}

func turnDiffUpdated(n notification, _ time.Time) []ports.ChatEvent {
	// The turn's running diff. Re-sent whole on every update (observed six times
	// with byte-identical payloads in one turn), so a projector must overwrite
	// per-turn state and must not append a timeline entry per update.
	var p codexproto.TurnDiffUpdatedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	files, truncated := parseTurnDiff(p.Diff)
	if len(files) == 0 {
		// An empty diff is a real state: the turn has changed nothing on disk
		// yet. Reporting it keeps a stale file list from surviving an undo.
		return []ports.ChatEvent{{
			Kind:           ports.ChatEventTurnDiff,
			ProviderTurnID: p.TurnID,
			Diff:           &ports.ChatTurnDiff{},
		}}
	}
	ev := ports.ChatEvent{
		Kind:           ports.ChatEventTurnDiff,
		ProviderTurnID: p.TurnID,
		Diff:           &ports.ChatTurnDiff{Files: files},
	}
	if truncated {
		// Carried in Summary because ChatTurnDiff has nowhere to say it, and a
		// cut list presented as a whole one would understate the change.
		ev.Summary = domain.ChatDiffTruncatedSummary
	}
	return []ports.ChatEvent{ev}
}

// The generated constant carries the provider's own deprecation note, and reading
// it anyway is the point: 0.146.0 does not send it, but a build old enough to do
// so would otherwise have its compactions silently dropped.
func threadCompacted(n notification, _ time.Time) []ports.ChatEvent {
	// The deprecated spelling, kept for a provider build old enough to send it.
	// A build that sends both this and the contextCompaction item would report
	// one compaction twice, so the conversation dedupes on the turn id.
	var p codexproto.ContextCompactedNotification
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:           ports.ChatEventCompacted,
		ProviderTurnID: p.TurnID,
	}}
}

func serverRequestResolved(n notification, _ time.Time) []ports.ChatEvent {
	var p resolvedEnvelope
	if err := json.Unmarshal(n.Params, &p); err != nil {
		return nil
	}
	id := rawID(p.RequestID)
	if id == "" {
		id = rawID(p.ID)
	}
	if id == "" {
		return nil
	}
	return []ports.ChatEvent{{
		Kind:      ports.ChatEventApprovalResolved,
		RequestID: id,
	}}
}

// turnIDFallback reads a top-level turnId, which some builds send instead of
// nesting the id under `turn`. Read from the raw params because codexproto's
// generated notification types declare only the nested form.
func turnIDFallback(params json.RawMessage) string {
	var p struct {
		TurnID string `json:"turnId"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return p.TurnID
}

// planFrom converts a provider plan into Tallpine's structured form.
//
// An unrecognized step status becomes pending rather than being carried through:
// the statuses drive whether a step reads as done, and inventing "done" for a word
// Tallpine does not know would tick off work that may not have happened.
func planFrom(p codexproto.TurnPlanUpdatedNotification) domain.ConversationPlan {
	plan := domain.ConversationPlan{Steps: make([]domain.ConversationPlanStep, 0, len(p.Plan))}
	if p.Explanation != nil {
		plan.Explanation = strings.TrimSpace(*p.Explanation)
	}
	for _, step := range p.Plan {
		text := strings.TrimSpace(step.Step)
		if text == "" {
			continue
		}
		plan.Steps = append(plan.Steps, domain.ConversationPlanStep{
			Text:   text,
			Status: planStepStatus(step.Status),
		})
	}
	return plan
}

func planStepStatus(status codexproto.TurnPlanStepStatus) domain.PlanStepStatus {
	switch status {
	case codexproto.TurnPlanStepStatusInProgress:
		return domain.PlanStepInProgress
	case codexproto.TurnPlanStepStatusCompleted:
		return domain.PlanStepCompleted
	default:
		return domain.PlanStepPending
	}
}

// planSummary labels a plan for a collapsed timeline row. It states progress
// rather than restating the first step, because progress is the thing that changes
// between the three or four updates one turn produces.
func planSummary(plan domain.ConversationPlan) string {
	if len(plan.Steps) == 0 {
		return "Cleared the plan"
	}
	done := 0
	current := ""
	for _, step := range plan.Steps {
		if step.Status == domain.PlanStepCompleted {
			done++
		}
		if current == "" && step.Status == domain.PlanStepInProgress {
			current = step.Text
		}
	}
	if current != "" {
		return fmt.Sprintf("Plan %d/%d: %s", done, len(plan.Steps), truncateLabel(current))
	}
	return fmt.Sprintf("Plan %d/%d steps done", done, len(plan.Steps))
}
