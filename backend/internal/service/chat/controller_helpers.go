package chat

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// planItemID keys a turn's plan activity. The provider sends no item id with a plan
// update — a plan is not an item — so the key is synthesized from the turn, which
// is what makes repeated updates rewrite one row. The prefix keeps it from ever
// colliding with a provider item id.
func planItemID(providerTurnID string) string {
	if providerTurnID == "" {
		return ""
	}
	return "ao-plan-" + providerTurnID
}

// rerouteItemID keys a reroute notice the same way, for the same reason.
func rerouteItemID(providerTurnID string) string {
	if providerTurnID == "" {
		return "ao-reroute"
	}
	return "ao-reroute-" + providerTurnID
}

// planActivityStatus reports a plan as still running until every step is done.
// Status here is about the plan, not about the turn: a plan with work left is not a
// completed thing, and showing it as one would tick off steps the agent is still on.
func planActivityStatus(plan domain.ConversationPlan) domain.ActivityStatus {
	if len(plan.Steps) == 0 {
		return domain.ActivityStatusCompleted
	}
	for _, step := range plan.Steps {
		if step.Status != domain.PlanStepCompleted {
			return domain.ActivityStatusRunning
		}
	}
	return domain.ActivityStatusCompleted
}

// planDetail is the plan's typed payload. The steps go in as structure rather than
// as rendered text: a client that wants checkboxes needs the per-step status, and a
// client that wants prose can join them, but the reverse is not recoverable.
func planDetail(plan domain.ConversationPlan) []byte {
	steps := make([]map[string]any, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		steps = append(steps, map[string]any{"text": step.Text, "status": string(step.Status)})
	}
	detail := map[string]any{"event": "plan", "steps": steps}
	if plan.Explanation != "" {
		detail["explanation"] = plan.Explanation
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

// rerouteDetail is the reroute notice's typed payload.
func rerouteDetail(reroute ports.ChatModelReroute) []byte {
	detail := map[string]any{
		"event":   "model.rerouted",
		"toModel": reroute.ToModel,
	}
	if reroute.FromModel != "" {
		detail["fromModel"] = reroute.FromModel
	}
	if reroute.Reason != "" {
		detail["reason"] = reroute.Reason
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return nil
	}
	return encoded
}

// mergeApprovalDetail folds the provider's offered decisions into the activity
// payload, so the client renders buttons from what the provider actually allows
// rather than from a fixed set.
func mergeApprovalDetail(event ports.ChatEvent) []byte {
	merged := map[string]any{}
	if len(event.Detail) > 0 {
		_ = json.Unmarshal(event.Detail, &merged)
	}
	decisions := make([]map[string]string, 0, len(event.Decisions))
	for _, option := range event.Decisions {
		decision := map[string]string{"id": option.ID, "label": option.Label}
		if option.Kind != "" {
			decision["kind"] = string(option.Kind)
		}
		decisions = append(decisions, decision)
	}
	merged["decisions"] = decisions
	encoded, err := json.Marshal(merged)
	if err != nil {
		return event.Detail
	}
	return encoded
}

func normalizeOrigin(origin domain.MessageOrigin) domain.MessageOrigin {
	switch origin {
	case domain.MessageOriginHuman, domain.MessageOriginAutomation,
		domain.MessageOriginDaemon, domain.MessageOriginProvider:
		return origin
	default:
		return domain.MessageOriginHuman
	}
}

// compactionDetail stamps the driver's reclaim figures with what kind of system
// event this row is.
//
// `system` is a general bucket, so a reader cannot tell a compaction from whatever
// else lands there next. The discriminator is set here rather than in a driver
// because the choice of kind is this projection's, not the provider's — and a
// compaction rendered as a generic notice would lose the one thing that matters
// about it: that the history above it is no longer what the agent sees.
func compactionDetail(event ports.ChatEvent) []byte {
	merged := map[string]any{}
	if len(event.Detail) > 0 {
		_ = json.Unmarshal(event.Detail, &merged)
	}
	merged["event"] = "compaction"
	encoded, err := json.Marshal(merged)
	if err != nil {
		return event.Detail
	}
	return encoded
}

func nonEmptyJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("null")
	}
	return raw
}

// reportActivity feeds the lifecycle reduction that derives user-facing status.
//
// Chat uses the same pipeline terminal sessions use rather than persisting a
// second display status — Tallpine derives status from durable facts at read time, and
// a parallel chat-only status would be a second source of truth to keep in sync.
//
// Best-effort: a rejected signal must not stop the durable projection, which is
// the record that actually matters. Lifecycle also rejects signals it considers
// stale, which is a legitimate outcome rather than an error to surface.
func (c *Controller) reportActivity(
	ctx context.Context,
	state domain.ActivityState,
	event string,
	now time.Time,
) {
	c.reportActivitySignal(ctx, ports.ActivitySignal{State: state, Event: event, Timestamp: now})
}

// reportActivitySignal sends a lifecycle signal fenced by this controller's
// generation. FinalAssistantMessage only feeds turn notifications; lifecycle
// never promotes it to a replay checkpoint.
func (c *Controller) reportActivitySignal(ctx context.Context, signal ports.ActivitySignal) {
	if c.activity == nil || c.reviewID != "" {
		return
	}
	signal.Valid = true
	signal.ControllerGeneration = c.generation
	if signal.State == domain.ActivityExited {
		c.mu.Lock()
		signal.IntentionalStop = c.explicitClose
		c.mu.Unlock()
	}
	if err := c.activity.ApplyActivitySignal(ctx, c.sessionID, signal); err != nil {
		c.log.Debug("chat activity signal rejected",
			"session", c.sessionID, "event", signal.Event, "error", err)
	}
}

// maxFinalAssistantBytes bounds the retained message; notifications quote far
// less than this.
const maxFinalAssistantBytes = 16 << 10

// finalAssistantText accumulates the latest root assistant message of a turn.
type finalAssistantText struct {
	turnID, itemID string
	text           strings.Builder
}

// observeAssistantText records committed root assistant text. Nested-thread
// messages (Codex child agents) are not the turn's answer.
func (c *Controller) observeAssistantText(event ports.ChatEvent) {
	if event.Kind != ports.ChatEventMessageDelta && event.Kind != ports.ChatEventMessageCompleted {
		return
	}
	if event.ProviderConversationID != "" && event.ProviderConversationID != c.conv.ProviderConversationID() {
		return
	}
	f := &c.finalAssistant
	if f.turnID != event.ProviderTurnID || f.itemID != event.ProviderItemID {
		f.turnID, f.itemID = event.ProviderTurnID, event.ProviderItemID
		f.text.Reset()
	}
	if event.Kind == ports.ChatEventMessageCompleted {
		if strings.TrimSpace(event.Text) == "" {
			return // nothing settled: the streamed text stands
		}
		f.text.Reset()
		event.Delta = event.Text
	}
	if room := maxFinalAssistantBytes - f.text.Len(); room > 0 {
		f.text.WriteString(truncateUTF8Head(event.Delta, room))
	}
}

func (f *finalAssistantText) peek(turnID string) string {
	if f.turnID == turnID {
		return strings.TrimSpace(f.text.String())
	}
	return ""
}

// take returns and clears the text held for turnID. Text from another turn is
// stale and never answers this one.
func (f *finalAssistantText) take(turnID string) string {
	text := ""
	if f.turnID == turnID {
		text = strings.TrimSpace(f.text.String())
	}
	f.turnID, f.itemID = "", ""
	f.text.Reset()
	return text
}
