package turnnotify

import (
	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// KindContextThrash labels the notice for a turn the agent stopped because its
// auto-compaction thrashed (B732). The worker did not choose to stop and every
// new turn refills the same context, so the orchestrator must respawn it on a
// larger-window model instead of nudging it. Unlike a plain turn end it is
// actionable, so it is not quiet.
const KindContextThrash = "context_thrash"

const contextThrashNotice = "the worker's agent stopped its turn because auto-compaction is thrashing: " +
	"its context refills to the compaction limit right after each compact, so it cannot make progress and nudging it will not help; " +
	"respawn the task on a model with a larger context window (or a larger compaction window)"

// turnEndKind classifies a turn end by what its last text says stopped it: the
// context-thrash excerpt (durable row or live turn) becomes its own notice
// quoting the agent's stop text; anything else is a plain turn end.
func turnEndKind(last string) (kind, detail string) {
	if detail, ok := domain.ParseContextThrashFallbackExcerpt(last); ok {
		return KindContextThrash, detail
	}
	return KindTurnEnded, last
}

// turnEndMessage renders a turn end's notice under its classified kind.
func turnEndMessage(rec domain.SessionRecord, last string) string {
	kind, detail := turnEndKind(last)
	return Message(kind, rec, detail)
}

// liveTurnText is the text a live turn-end signal records: the agent's thrash
// stop when there is one, since the last assistant message is earlier work.
func liveTurnText(s ports.ActivitySignal, last string) string {
	if s.ContextThrash != "" {
		return domain.ContextThrashFallbackExcerpt(s.ContextThrash)
	}
	return last
}
