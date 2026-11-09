package turnnotify

import (
	"context"
	"strings"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// lastMessageLabel introduces a worker's quoted text in a synthesized report.
const lastMessageLabel = "\nLast message: "

// heldTurnReports is the optional store capability behind the third loss window
// of UndeliveredTurnResult.
type heldTurnReports interface {
	// LatestHeldTurnEndedReport returns the message of the session's newest
	// turn_ended report still held undelivered in the quiet window.
	LatestHeldTurnEndedReport(context.Context, domain.SessionID) (string, bool, error)
}

// UndeliveredTurnResult renders the worker's last turn result if closing the
// session now would lose it, or "" if there is none (B557). A close drops the
// result in three places: the Stop signal that closes the session is still being
// applied, so turnEnded will find it terminated; the idle timer or durable row
// has not yet produced its report and sendWithID refuses terminated sessions;
// or the report exists but is held quiet, and quietStaleReason discards held
// reports of terminated senders.
//
// It only reads: a close that does not go through leaves the normal notice armed.
func (o *Observer) UndeliveredTurnResult(ctx context.Context, rec domain.SessionRecord) string {
	if rec.Kind != domain.KindWorker || o.isClosed() {
		return ""
	}
	if last := o.liveTurnResult(rec.ID); last != "" {
		return Message(KindTurnEnded, rec, last)
	}
	if last := o.pendingFallbackResult(ctx, rec.ID); last != "" {
		return Message(KindTurnEnded, rec, last)
	}
	if held, ok := o.Fallbacks.(heldTurnReports); ok {
		message, found, err := held.LatestHeldTurnEndedReport(ctx, rec.ID)
		if err != nil {
			o.Log.Warn("read held turn report before close", "session", rec.ID, "err", err)
		} else if found && strings.Contains(message, lastMessageLabel) {
			return message
		}
	}
	return ""
}

// liveTurnResult is the text of a turn end still being applied or waiting out
// its idle timer, under the same rules that decide whether it gets a notice.
func (o *Observer) liveTurnResult(id domain.SessionID) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.turns[id]
	if t != nil && t.finalReported {
		return ""
	}
	if last := o.finishing[id]; last != "" {
		if t != nil && !t.approvedAt.IsZero() && o.Now().Sub(t.approvedAt) <= approvalCoverWindow {
			return ""
		}
		return last
	}
	if t != nil && t.timer != nil && !t.notified {
		return t.last
	}
	return ""
}

// pendingFallbackResult is the newest durable completion not yet turned into a report.
func (o *Observer) pendingFallbackResult(ctx context.Context, id domain.SessionID) string {
	if o.Fallbacks == nil {
		return ""
	}
	pending, err := o.Fallbacks.ListPendingTurnFallbacks(ctx)
	if err != nil {
		o.Log.Warn("read pending turn fallbacks before close", "session", id, "err", err)
		return ""
	}
	for i := len(pending) - 1; i >= 0; i-- {
		f := pending[i]
		if _, _, limited := domain.ParseUsageLimitFallbackExcerpt(f.Excerpt); f.SessionID == id && !limited && strings.TrimSpace(f.Excerpt) != "" {
			return f.Excerpt
		}
	}
	return ""
}

// trackFinishing exposes a turn-ending signal's text while the pipeline applies
// it: lifecycle closes a finished worker synchronously inside that signal,
// before the observer records the turn end. The returned func clears it.
func (o *Observer) trackFinishing(id domain.SessionID, s ports.ActivitySignal) func() {
	last := finishingText(s)
	if last == "" {
		return func() {}
	}
	o.mu.Lock()
	o.finishing[id] = last
	o.mu.Unlock()
	return func() {
		o.mu.Lock()
		delete(o.finishing, id)
		o.mu.Unlock()
	}
}

// finishingText is the result text of a signal that ends a main-agent turn.
func finishingText(s ports.ActivitySignal) string {
	if s.SubagentID != "" || nonInteractive(s.NativeEntrypoint) || !s.UsageLimitPausedUntil.IsZero() {
		return ""
	}
	if s.Event != "stop" && s.Event != chatTurnCompleted {
		return ""
	}
	if s.Event == "stop" && s.RunningSubagentIDs != nil && len(*s.RunningSubagentIDs) > 0 {
		return ""
	}
	return strings.TrimSpace(s.FinalAssistantMessage)
}
