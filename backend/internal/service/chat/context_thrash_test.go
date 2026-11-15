package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// thrashStop is the error claude-agent-acp fails a turn with when Claude Code's
// auto-compaction breaker trips (the AIR session failure title).
const thrashStop = "Autocompact is thrashing: the context refilled to the limit within 3 turns of the previous compact, 3 times in a row. " +
	"A file being read or a tool output is likely too large for the context window. Try reading in smaller chunks, or use /clear to start fresh."

// B732: the thrash stop reaches the turn notice through both paths, the live
// signal and the durable fallback row, instead of the sentence the worker wrote
// before the stop.
func TestContextThrashTurnCarriesStopToTurnNotice(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	ctx := context.Background()
	worker, found, err := h.st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("worker fixture: found=%v err=%v", found, err)
	}
	worker.Kind = domain.KindWorker
	if err = h.st.UpdateSession(ctx, worker); err != nil {
		t.Fatal(err)
	}
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "work", ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	const provider, thread = "provider-turn-1", "thread-1"
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: provider, ProviderConversationID: thread})
	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventMessageCompleted, ProviderTurnID: provider, ProviderConversationID: thread,
			ProviderItemID: "m1", Text: "Picking up at the HubApp mapping step."},
		ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: provider, ProviderConversationID: thread,
			TurnState: domain.TurnStateFailed, Err: errors.New(thrashStop)},
	)
	failedTurnSnapshot(t, h, turn.ID)
	if signal := awaitTurnCompletedSignal(t, h.activity); signal.ContextThrash != thrashStop {
		t.Fatalf("signal context thrash = %q", signal.ContextThrash)
	}
	pending, err := h.st.ListPendingTurnFallbacks(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("fallbacks = %+v, %v", pending, err)
	}
	if detail, ok := domain.ParseContextThrashFallbackExcerpt(pending[0].Excerpt); !ok || detail != domain.TurnFallbackExcerpt(thrashStop) {
		t.Fatalf("excerpt = %q, want the thrash stop", pending[0].Excerpt)
	}
}
