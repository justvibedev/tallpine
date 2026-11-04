package chat_test

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func compactionEvent(c ports.ChatCompaction) ports.ChatEvent {
	return ports.ChatEvent{Kind: ports.ChatEventThreadState, ThreadState: &ports.ChatThreadState{Compaction: &c}}
}

// A compaction report is recorded on the thread state, enriched (not re-timed) by
// a second terminal report, and leaves the status alone.
func TestCompactionLifecycleIsRecorded(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventThreadState, ThreadState: &ports.ChatThreadState{Status: domain.ThreadStatusActive}},
		compactionEvent(ports.ChatCompaction{ID: "c1", Status: domain.CompactionRunning}),
	)
	running := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		ts := s.Conversation.ThreadState
		return ts != nil && ts.Compaction != nil
	})
	if got := running.Conversation.ThreadState.Compaction; got.Status != domain.CompactionRunning {
		t.Fatalf("compaction = %+v", got)
	}
	if running.Conversation.ThreadState.Status != domain.ThreadStatusActive {
		t.Fatalf("a compaction report changed the status: %+v", running.Conversation.ThreadState)
	}

	h.conv.emit(compactionEvent(ports.ChatCompaction{ID: "c1", Status: domain.CompactionCompleted, Trigger: "manual", PreTokens: 410000}))
	done := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.ThreadState.Compaction.Status == domain.CompactionCompleted
	})
	at := done.Conversation.ThreadState.Compaction.At
	h.advance(time.Hour)

	h.conv.emit(compactionEvent(ports.ChatCompaction{ID: "c1", Status: domain.CompactionCompleted, Trigger: "manual", PreTokens: 410000, PostTokens: 62000}))
	enriched := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.ThreadState.Compaction.PostTokens == 62000
	})
	got := enriched.Conversation.ThreadState.Compaction
	if got.Trigger != "manual" || got.PreTokens != 410000 || !got.At.Equal(at) {
		t.Fatalf("enriched = %+v (finished at %v)", got, at)
	}
}

// A terminal-first report of a later compaction is not an enrichment of the
// earlier one: it takes its own time and inherits nothing.
func TestCompactionOfAnotherCallIsNotMerged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)

	h.conv.emit(compactionEvent(ports.ChatCompaction{ID: "c1", Status: domain.CompactionCompleted, Trigger: "manual", PreTokens: 410000, PostTokens: 62000}))
	first := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		ts := s.Conversation.ThreadState
		return ts != nil && ts.Compaction != nil
	})
	h.advance(time.Hour)
	h.conv.emit(compactionEvent(ports.ChatCompaction{ID: "c2", Status: domain.CompactionCompleted}))
	second := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.ThreadState.Compaction.ID == "c2"
	})
	got := second.Conversation.ThreadState.Compaction
	if got.Trigger != "" || got.PreTokens != 0 || got.PostTokens != 0 || !got.At.After(first.Conversation.ThreadState.Compaction.At) {
		t.Fatalf("second compaction = %+v, first = %+v", got, first.Conversation.ThreadState.Compaction)
	}
}
