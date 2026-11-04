package chat_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

const planAnswerPrompt = "The owner answered. Write and submit the plan now with `tallpine plan submit B310`."

// B329: the owner answers a planner's questions, the provider turn ends on the
// account's usage limit, and the daemon restarts (or the chat hibernates) before
// the reset. The answer must reach the planner once, at the reset, with no
// account failover, and the planner must read as paused until then.
func TestUsageLimitPlanAnswerThenLimitThenResetResumesPlanner(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	restartAsKind(t, h, domain.KindWorker)
	ctx := context.Background()
	turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: planAnswerPrompt, ClientMessageID: uuid.NewString(), Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: turn.ProviderTurnID,
		TurnState: domain.TurnStateFailed, Err: errors.New(timedUsageLimit)})
	failedTurnSnapshot(t, h, turn.ID)
	awaitUsageLimitProjection(t, h.ctrl)

	armed, err := h.st.TurnByID(ctx, turn.ID)
	if err != nil || armed.UsageLimit == nil || !armed.UsageLimit.AutoResume {
		t.Fatalf("limit = %+v, %v, want armed without the owner clicking", armed.UsageLimit, err)
	}
	until, paused := h.svc.UsageLimitPausedUntil(ctx, testSession)
	if !paused || !until.Equal(*armed.UsageLimit.ResetsAt) {
		t.Fatalf("paused = %v until %v, want the planner paused until the reset %v", paused, until, armed.UsageLimit.ResetsAt)
	}

	// The daemon restarts: a new service and provider process know the turn only from the store.
	restartAsKind(t, h, domain.KindWorker)
	h.conv.turnSeq = 1 // a new provider process numbers its turns after the stored ones
	if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.conv.sentTexts(); len(got) != 0 {
		t.Fatalf("sent before the reset: %v", got)
	}

	h.advance(time.Minute)
	for range 2 {
		if err := h.svc.ResumeUsageLimitedTurns(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.conv.sentTexts(); len(got) != 1 || got[0] != planAnswerPrompt {
		t.Fatalf("sent = %v, want the owner's answer replayed once at the reset", got)
	}
}
