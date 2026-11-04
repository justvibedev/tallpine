package chat_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/lifecycle"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
)

func TestInterfaceHandoffRefreshesNativeHistoryUntilSettledBeforeStartingChat(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Finish the work."
	rec.Metadata.LatestAssistantUpdate = "Settled answer."
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed native replay checkpoint: %v", err)
	}
	conv := &convergingHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-user-1", Text: "Finish the work.",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-answer-1", Text: "Settled answer.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-complete",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateCompleted,
			},
		},
	}
	var idMu sync.Mutex
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("converging-history-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start handoff: %v", err)
	}
	reads, refreshes := conv.historyAttempts()
	if reads != 1 || refreshes != 1 {
		t.Fatalf("history attempts = %d reads, %d refreshes; want one initial read followed by one refresh",
			reads, refreshes)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Messages) != 2 || snapshot.Messages[0].Text != "Finish the work." ||
		snapshot.Messages[1].Text != "Settled answer." {
		t.Fatalf("messages = %#v, want checkpoint prompt and settled native answer", snapshot.Messages)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateCompleted {
		t.Fatalf("turns = %#v, want one completed native turn", snapshot.Turns)
	}
}

func TestInterfaceHandoffRefreshesNativeHistoryUntilItReachesTheCheckpoint(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Run the final verification."
	rec.Metadata.LatestAssistantUpdate = "The final verification passed."
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed native replay checkpoint: %v", err)
	}

	conv := &convergingHistoryConversation{
		fakeConversation: newFakeConversation(),
		initialSettled:   true,
		// The first authoritative observation is internally settled but stale.
		// RefreshHistory performs the provider read that reaches Tallpine's checkpoint.
		initialEvents: nil,
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-user-1",
				Text: "Run the final verification.",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-answer-1",
				Text: "The final verification passed.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-complete",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateCompleted,
			},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("checkpoint-refresh-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start handoff: %v", err)
	}
	reads, refreshes := conv.historyAttempts()
	if reads != 1 || refreshes != 1 {
		t.Fatalf("history attempts = %d reads, %d refreshes; want one stale read followed by one refresh",
			reads, refreshes)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Messages) != 2 || snapshot.Messages[0].Text != "Run the final verification." ||
		snapshot.Messages[1].Text != "The final verification passed." {
		t.Fatalf("messages = %#v, want refreshed checkpoint transcript", snapshot.Messages)
	}
}

// A provider that rejects the replay outright must end the settle wait on the
// first refresh instead of polling until nativeHistorySettleLimit.
func TestInterfaceHandoffStopsPollingWhenProviderRejectsHistoryLoad(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := &rejectedHistoryConversation{fakeConversation: newFakeConversation()}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("load-rejected-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	started := time.Now()
	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("Start error = %v, want ErrChatHistoryLoadFailed", err)
	}
	if got := conv.refreshes.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want exactly one rejected provider observation", got)
	}
	if elapsed := time.Since(started); elapsed > 20*time.Second {
		t.Fatalf("settle wait took %v, want an early exit well under the 45s settle limit", elapsed)
	}
}

// A session/load that never returns must be cut off by the per-attempt bound
// and reported as a load failure while the settle budget is still live, instead
// of holding the transition for the whole nativeHistorySettleLimit.
func TestInterfaceHandoffBoundsStalledHistoryLoadAttempt(t *testing.T) {
	restore := chatsvc.SetNativeHistoryLoadAttemptLimit(200 * time.Millisecond)
	t.Cleanup(restore)
	st := openStore(t)
	conv := &stalledHistoryConversation{fakeConversation: newFakeConversation()}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("load-stalled-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	started := time.Now()
	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessClaudeCode,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryLoadFailed) {
		t.Fatalf("Start error = %v, want ErrChatHistoryLoadFailed", err)
	}
	if errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, must not read as a retryable unsettled wait", err)
	}
	if got := conv.refreshes.Load(); got != 1 {
		t.Fatalf("refreshes = %d, want the single bounded attempt", got)
	}
	if !conv.refreshCtxDone.Load() {
		t.Fatal("refresh context never ended; the attempt bound did not cancel the load")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("settle wait took %v, want the 200ms attempt bound, not the 45s settle limit", elapsed)
	}
}

func TestInterfaceHandoffImportsInterruptedUserOnlyNativeHistory(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-user-1",
				Text: "Tallpine transferred the previous agent's context in hidden system instructions.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-interrupted",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateInterrupted,
			},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("interrupted-history-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start handoff: %v", err)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Text != conv.events[1].Text {
		t.Fatalf("messages = %#v, want preserved interrupted handoff prompt", snapshot.Messages)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateInterrupted {
		t.Fatalf("turns = %#v, want one interrupted native turn", snapshot.Turns)
	}
}

func TestInterfaceHandoffImportsOutcomeUnknownNativeHistoryAsRecovered(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-user-1",
				Text: "Historical work with no portable provider outcome.",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-answer-1",
				Text: "Partial or complete historical output.",
			},
			{
				Kind: ports.ChatEventActivityCompleted, ProviderEventID: "history-tool",
				ProviderTurnID: "native-turn-1", ProviderItemID: "native-tool-1",
				ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusRecovered,
				Summary: "Historical command with no portable outcome",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-recovered",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateRecovered,
			},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("recovered-history-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start handoff: %v", err)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	if len(snapshot.Turns) != 1 || snapshot.Turns[0].State != domain.TurnStateRecovered ||
		!snapshot.Turns[0].State.Terminal() {
		t.Fatalf("snapshot = turns %#v messages %#v activities %#v, want one terminal recovered native turn",
			snapshot.Turns, snapshot.Messages, snapshot.Activities)
	}
	if len(snapshot.Activities) != 1 || snapshot.Activities[0].Status != domain.ActivityStatusRecovered {
		t.Fatalf("activities = %#v, want one recovered historical activity", snapshot.Activities)
	}
}

func TestInterfaceHandoffRejectsAProviderWithoutNativeHistoryReplay(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := newFakeConversation()
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("no-history-%d", time.Now().UnixNano()) },
	})

	_, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnavailable) {
		t.Fatalf("Start error = %v, want ErrChatHistoryUnavailable", err)
	}
	if _, controllerErr := svc.Controller(testSession); !errors.Is(controllerErr, chatsvc.ErrNoController) {
		t.Fatalf("Controller error = %v, want no target controller after failed replay", controllerErr)
	}
}

func TestInterfaceHandoffReportsUnsettledHistoryWhenContextEndsBeforeRefresh(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conv := &convergingHistoryConversation{
		fakeConversation: newFakeConversation(),
		// End the request only after native history import is reached. A tiny
		// wall-clock deadline here used to expire during SQLite setup under the
		// race runner and test ClaimChatControllerGeneration instead.
		onRead: cancel,
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("unsettled-%d", time.Now().UnixNano()) },
	})
	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want ErrChatHistoryUnsettled", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context cancellation cause", err)
	}
	reads, refreshes := conv.historyAttempts()
	if reads != 1 || refreshes != 0 {
		t.Fatalf("history attempts = %d reads, %d refreshes; want cancellation before refresh",
			reads, refreshes)
	}
}

func TestInterfaceHandoffRejectsUnsettledImmutableHistoryWithoutRereading(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		err:              ports.ErrChatHistoryUnsettled,
		// Cancel a forbidden second read so the test fails quickly instead of
		// waiting the full 45s settle limit on a regression.
		onRead: func(reads int) {
			if reads == 2 {
				cancel()
			}
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("immutable-unsettled-%d", time.Now().UnixNano()) },
	})
	_, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want ErrChatHistoryUnsettled", err)
	}
	if reads := conv.historyReads(); reads != 1 {
		t.Fatalf("immutable history reads = %d, want exactly one", reads)
	}
}

func TestInterfaceHandoffRejectsSettledReplayBeforeLatestSessionCheckpoint(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Run the final verification."
	rec.Metadata.LatestAssistantUpdate = "The final verification passed."
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed native replay checkpoint: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		// A provider may report a syntactically settled but stale prefix while its
		// on-disk transcript is still being flushed. Empty is the strongest form of
		// that failure: no replay event reaches the hook facts Tallpine already observed.
		events: nil,
		// Cancel a forbidden second read so the test fails quickly instead of
		// waiting the full 45s settle limit on a regression.
		onRead: func(reads int) {
			if reads == 2 {
				cancel()
			}
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("checkpoint-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want ErrChatHistoryUnsettled for stale settled replay", err)
	}
	if !ports.ChatHistoryMismatchOnlyUntrustedText(err) {
		t.Fatalf("Start mismatch = %v, want only untrusted legacy text dimensions", err)
	}
	if reads := conv.historyReads(); reads != 1 {
		t.Fatalf("immutable history reads = %d, want exactly one", reads)
	}
}

func TestInterfaceHandoffCheckpointHistoryPolicy(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		state         domain.ConversationCheckpointState
		policy        domain.SessionInterfaceTransitionHistoryPolicy
		wantMismatch  bool
		wantUntrusted bool
	}{
		{"explicit provider history ignores legacy text", domain.ConversationCheckpointLegacy, domain.SessionInterfaceTransitionHistoryProvider, false, false},
		{"strict coordination retains preceding human text", domain.ConversationCheckpointCoordination, domain.SessionInterfaceTransitionHistoryStrict, true, true},
		{"coordination text without trust requires consent to bypass", domain.ConversationCheckpointCoordination, domain.SessionInterfaceTransitionHistoryProvider, false, false},
		{"provider history cannot waive trusted text", domain.ConversationCheckpointComplete, domain.SessionInterfaceTransitionHistoryProvider, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openStore(t)
			rec, found, err := st.GetSession(context.Background(), testSession)
			if err != nil || !found {
				t.Fatalf("load session: found=%v err=%v", found, err)
			}
			rec.Metadata.LatestUserPrompt = "user checkpoint absent from provider history"
			rec.Metadata.LatestAssistantUpdate = "assistant checkpoint absent from provider history"
			rec.Metadata.ConversationCheckpointState = tc.state
			if tc.state != domain.ConversationCheckpointLegacy {
				rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
				rec.Metadata.ConversationCheckpointNativeID = "thread-1"
			}
			if err := st.UpdateSession(context.Background(), rec); err != nil {
				t.Fatalf("seed checkpoint: %v", err)
			}
			conv := &nativeHistoryConversation{fakeConversation: newFakeConversation()}
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return fmt.Sprintf("checkpoint-history-%d", time.Now().UnixNano()) },
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
			ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
				HistoryPolicy: tc.policy,
			})
			if !tc.wantMismatch {
				if err != nil || ctrl == nil {
					t.Fatalf("Start = %v, %v; want a live controller", ctrl, err)
				}
				return
			}
			if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatalf("Start error = %v, want trusted checkpoint mismatch", err)
			}
			if ports.ChatHistoryMismatchOnlyUntrustedText(err) != tc.wantUntrusted {
				t.Fatalf("wrong checkpoint trust classification: %v", err)
			}
			if tc.wantUntrusted {
				return
			}
			dimensions := ports.ChatHistoryMismatchDimensions(err)
			if !slices.Contains(dimensions, ports.ChatHistoryMismatchTrustedUserText) ||
				!slices.Contains(dimensions, ports.ChatHistoryMismatchTrustedAssistantText) {
				t.Fatalf("trusted mismatch dimensions = %v", dimensions)
			}
		})
	}
}

func TestInterfaceHandoffTrustedCheckpointMayPrecedeLaterCompletedTurn(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		state        domain.ConversationCheckpointState
		assistant    string
		turnID       string
		emptyPrompt  bool
		unsettled    bool
		wantMismatch bool
	}{
		{name: "unidentified completed pair stays latest", state: domain.ConversationCheckpointComplete, assistant: "trusted checkpoint assistant", wantMismatch: true},
		{name: "identified completed pair", state: domain.ConversationCheckpointComplete, assistant: "trusted checkpoint assistant", turnID: "checkpoint-turn"},
		{name: "completed Codex prompt", state: domain.ConversationCheckpointComplete, turnID: "checkpoint-turn"},
		{name: "completed empty prompt identified", state: domain.ConversationCheckpointComplete, turnID: "checkpoint-turn", emptyPrompt: true},
		{name: "missing completed empty prompt", state: domain.ConversationCheckpointComplete, turnID: "missing-current-turn", emptyPrompt: true, wantMismatch: true},
		{name: "missing pending empty prompt", state: domain.ConversationCheckpointPrompt, turnID: "missing-current-turn", emptyPrompt: true, wantMismatch: true},
		{name: "pending empty prompt stays latest", state: domain.ConversationCheckpointPrompt, turnID: "checkpoint-turn", emptyPrompt: true, wantMismatch: true},
		{name: "old prompt-only checkpoint stays strict", state: domain.ConversationCheckpointComplete, wantMismatch: true},
		{name: "repeated prompt cannot stand in for missing current turn", state: domain.ConversationCheckpointComplete, turnID: "missing-current-turn", wantMismatch: true},
		{name: "pair cannot stand in for missing current turn", state: domain.ConversationCheckpointComplete, turnID: "missing-current-turn", assistant: "trusted checkpoint assistant", wantMismatch: true},
		{name: "pending prompt cannot match older turn", state: domain.ConversationCheckpointPrompt, wantMismatch: true},
		{name: "unmatched newer stop still blocks", state: domain.ConversationCheckpointComplete, unsettled: true, wantMismatch: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			rec, found, err := st.GetSession(context.Background(), testSession)
			if err != nil || !found {
				t.Fatalf("load session: found=%v err=%v", found, err)
			}
			// Tallpine may have no hook evidence at all for a later provider turn. In that case
			// the earlier coherent checkpoint still need not be the replay's final turn.
			// A later scoped Stop is covered separately and becomes a latest-turn gate.
			rec.Metadata.LatestUserPrompt = "trusted checkpoint user"
			if tt.emptyPrompt {
				rec.Metadata.LatestUserPrompt = ""
			}
			rec.Metadata.LatestAssistantUpdate = tt.assistant
			rec.Metadata.ConversationCheckpointState = tt.state
			rec.Metadata.ConversationCheckpointTurnID = tt.turnID
			rec.Metadata.ConversationCheckpointUnsettled = tt.unsettled
			rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
			rec.Metadata.ConversationCheckpointNativeID = "thread-1"
			if err := st.UpdateSession(context.Background(), rec); err != nil {
				t.Fatalf("seed trusted checkpoint: %v", err)
			}
			conv := &nativeHistoryConversation{
				fakeConversation: newFakeConversation(),
				events: []ports.ChatEvent{
					{Kind: ports.ChatEventTurnStarted, ProviderEventID: "checkpoint-start", ProviderTurnID: "checkpoint-turn"},
					{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "checkpoint-user", ProviderTurnID: "checkpoint-turn", ProviderItemID: "checkpoint-user-item", Text: "trusted checkpoint user"},
					{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "checkpoint-assistant", ProviderTurnID: "checkpoint-turn", ProviderItemID: "checkpoint-assistant-item", Text: "trusted checkpoint assistant"},
					{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "checkpoint-complete", ProviderTurnID: "checkpoint-turn", TurnState: domain.TurnStateCompleted},
					{Kind: ports.ChatEventTurnStarted, ProviderEventID: "later-start", ProviderTurnID: "later-turn"},
					{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "later-user", ProviderTurnID: "later-turn", ProviderItemID: "later-user-item", Text: "later prompt whose hook was lost"},
					{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "later-assistant", ProviderTurnID: "later-turn", ProviderItemID: "later-assistant-item", Text: "later answer whose prompt hook was lost"},
					{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "later-complete", ProviderTurnID: "later-turn", TurnState: domain.TurnStateCompleted},
				},
			}
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return fmt.Sprintf("earlier-checkpoint-%d", time.Now().UnixNano()) },
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

			ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
				HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
			})
			if tt.wantMismatch {
				if !errors.Is(err, ports.ErrChatHistoryUnsettled) || ports.ChatHistoryMismatchOnlyUntrustedText(err) {
					t.Fatalf("unsafe earlier checkpoint should fail closed, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start with earlier trusted checkpoint: %v", err)
			}
			snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
			if err != nil {
				t.Fatalf("LoadConversationSnapshot: %v", err)
			}
			if len(snapshot.Turns) != 2 || len(snapshot.Messages) != 4 {
				t.Fatalf("replayed snapshot = %d turns, %d messages; want both completed turns", len(snapshot.Turns), len(snapshot.Messages))
			}
		})
	}
}

func TestInterfaceHandoffPanePromptWithMissedHookCannotAcceptHistoryBeforeObservedStop(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	changed, err := st.CommitSessionControllerEpoch(
		context.Background(), testSession, domain.SessionModeChat, domain.SessionModeTUI,
		"thread-1", time.Now().UTC(),
	)
	if err != nil || !changed {
		t.Fatalf("commit initial TUI mode: changed=%v err=%v", changed, err)
	}
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.RuntimeLaunchID = "terminal-generation"
	rec.Metadata.AgentSessionID = "thread-1"
	rec.Metadata.AgentSessionIDLaunchID = "terminal-generation"
	rec.Metadata.LatestUserPrompt = "trusted checkpoint user"
	rec.Metadata.LatestAssistantUpdate = "trusted checkpoint assistant"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
	rec.Metadata.ConversationCheckpointNativeID = "thread-1"
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed trusted checkpoint: %v", err)
	}
	if changed, err := st.RecordSessionLatestUserPrompt(
		context.Background(), testSession, "later prompt whose hook was lost", time.Now().UTC(),
	); err != nil || !changed {
		t.Fatalf("record pane-delivered prompt: changed=%v err=%v", changed, err)
	}

	// Tallpine delivered another pane turn, but its UserPromptSubmit hook was missed.
	// The Stop still proves there is an unresolved completed boundary that
	// provider-history recovery must not waive.
	lifecycleManager := lifecycle.New(st, nil)
	if err := lifecycleManager.ApplyActivitySignal(context.Background(), testSession, ports.ActivitySignal{
		Valid: true, State: domain.ActivityIdle, Event: "stop",
		LaunchID: "terminal-generation", AgentSessionID: "thread-1",
		LatestAssistantUpdate: "later answer whose prompt hook was lost",
	}); err != nil {
		t.Fatalf("apply later Stop: %v", err)
	}
	// Another pane send is not a canonical provider prompt boundary. If its hook
	// is missed too, it must not erase the hard evidence established by Stop.
	if changed, err := st.RecordSessionLatestUserPrompt(
		context.Background(), testSession, "third prompt whose hook was also lost", time.Now().UTC(),
	); err != nil || !changed {
		t.Fatalf("record later pane-delivered prompt: changed=%v err=%v", changed, err)
	}
	changed, err = st.CommitSessionControllerEpoch(
		context.Background(), testSession, domain.SessionModeTUI, domain.SessionModeChat,
		"thread-1", time.Now().UTC(),
	)
	if err != nil || !changed {
		t.Fatalf("commit Chat mode: changed=%v err=%v", changed, err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "checkpoint-start", ProviderTurnID: "checkpoint-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "checkpoint-user", ProviderTurnID: "checkpoint-turn", ProviderItemID: "checkpoint-user-item", Text: "trusted checkpoint user"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "checkpoint-assistant", ProviderTurnID: "checkpoint-turn", ProviderItemID: "checkpoint-assistant-item", Text: "trusted checkpoint assistant"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "checkpoint-complete", ProviderTurnID: "checkpoint-turn", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("missed-prompt-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want replay to reject history ending before observed Stop", err)
	}
	if ports.ChatHistoryMismatchOnlyUntrustedText(err) {
		t.Fatalf("observed Stop mismatch was incorrectly recoverable: %v", err)
	}
}

func TestInterfaceHandoffImmutableBoundaryFailsBeforeReadingProviderHistory(t *testing.T) {
	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.ConversationCheckpointUnsettled = true
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed unresolved boundary: %v", err)
	}

	conv := &convergingHistoryConversation{
		fakeConversation: newFakeConversation(),
		initialSettled:   true,
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("immutable-boundary-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want immutable unresolved-boundary mismatch", err)
	}
	if ports.ChatHistoryMismatchOnlyUntrustedText(err) {
		t.Fatalf("immutable boundary was incorrectly recoverable: %v", err)
	}
	reads, refreshes := conv.historyAttempts()
	if reads != 0 || refreshes != 0 {
		t.Fatalf("history attempts = %d reads, %d refreshes; immutable boundary should fail before provider polling",
			reads, refreshes)
	}
}

func TestInterfaceHandoffAssistantOnlyCheckpointFailsClosedOnRepeatedText(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestAssistantUpdate = "Done."
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
	rec.Metadata.ConversationCheckpointNativeID = "thread-1"
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed assistant-only checkpoint: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "later-start", ProviderTurnID: "later-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "prior-user", ProviderTurnID: "later-turn", ProviderItemID: "prior-user-item", Text: "prior prompt"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "prior-assistant", ProviderTurnID: "later-turn", ProviderItemID: "prior-assistant-item", Text: "Done."},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "later-complete", ProviderTurnID: "later-turn", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("assistant-checkpoint-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want ambiguous assistant-only checkpoint to fail closed", err)
	}
	if ports.ChatHistoryMismatchOnlyUntrustedText(err) {
		t.Fatalf("ambiguous assistant-only checkpoint was incorrectly recoverable: %v", err)
	}
}

func TestInterfaceHandoffTrustedCheckpointMustMatchOneCompletedTurn(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "latest trusted user"
	rec.Metadata.LatestAssistantUpdate = "repeated trusted assistant"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
	rec.Metadata.ConversationCheckpointNativeID = "thread-1"
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed trusted checkpoint: %v", err)
	}
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "older-start", ProviderTurnID: "older-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "older-user", ProviderTurnID: "older-turn", ProviderItemID: "older-user-item", Text: "older user"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "older-assistant", ProviderTurnID: "older-turn", ProviderItemID: "older-assistant-item", Text: "repeated trusted assistant"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "older-complete", ProviderTurnID: "older-turn", TurnState: domain.TurnStateCompleted},
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "latest-start", ProviderTurnID: "latest-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "latest-user", ProviderTurnID: "latest-turn", ProviderItemID: "latest-user-item", Text: "latest trusted user"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "latest-complete", ProviderTurnID: "latest-turn", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("single-turn-checkpoint-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want trusted checkpoint mismatch across completed turns", err)
	}
	if dimensions := ports.ChatHistoryMismatchDimensions(err); !slices.Contains(dimensions, ports.ChatHistoryMismatchTrustedAssistantText) {
		t.Fatalf("mismatch dimensions = %v, want trusted assistant text", dimensions)
	}
}

func TestInterfaceHandoffTallpineHighWaterFallbackMustStayInItsTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 8, 26, 2, 0, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(
		ctx, "high-water-turn-conversation", domain.ConversationScopeSession,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, testSession, "chat-generation"); err != nil {
		t.Fatalf("claim generation: %v", err)
	}
	created, err := st.AppendUserMessage(
		ctx, conversation.ID, testSession, "chat-generation",
		domain.ConversationMessage{
			ID: "expected-user", Text: "expected user", Origin: domain.MessageOriginHuman,
			ClientMessageID: "expected-client",
		},
		"expected-turn", now,
	)
	if err != nil || !created {
		t.Fatalf("append user: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "expected-turn", "expected-provider-turn", now); err != nil {
		t.Fatalf("bind turn: %v", err)
	}
	if err := st.SettleAssistantMessage(
		ctx, conversation.ID, "expected-assistant-item", "expected-provider-turn",
		"repeated high water", "expected-assistant", now,
	); err != nil {
		t.Fatalf("settle assistant: %v", err)
	}
	if err := st.SettleTurn(
		ctx, conversation.ID, "expected-provider-turn", domain.TurnStateCompleted, "", now,
	); err != nil {
		t.Fatalf("settle turn: %v", err)
	}
	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "older-start", ProviderTurnID: "older-provider-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "older-user", ProviderTurnID: "older-provider-turn", ProviderItemID: "older-user-item", Text: "older user"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "older-assistant", ProviderTurnID: "older-provider-turn", ProviderItemID: "older-assistant-item", Text: "repeated high water"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "older-complete", ProviderTurnID: "older-provider-turn", TurnState: domain.TurnStateCompleted},
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "expected-start", ProviderTurnID: "expected-provider-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "expected-replayed-user", ProviderTurnID: "expected-provider-turn", ProviderItemID: "expected-replayed-user-item", Text: "expected user"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "expected-complete", ProviderTurnID: "expected-provider-turn", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns,
				Messages:     rows.Messages,
				Activities:   rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("high-water-turn-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	_, err = svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
		t.Fatalf("Start error = %v, want Tallpine high-water mismatch from its completed turn", err)
	}
	if dimensions := ports.ChatHistoryMismatchDimensions(err); !slices.Contains(dimensions, ports.ChatHistoryMismatchTallpineHighWater) {
		t.Fatalf("mismatch dimensions = %v, want Tallpine high water", dimensions)
	}
}

func TestInterfaceHandoffTallpineHighWaterAcceptsMappedReassignedTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 8, 26, 2, 30, 0, 0, time.UTC)
	conversation, err := st.CreateConversation(
		ctx, "high-water-reassigned-conversation", domain.ConversationScopeSession,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, testSession, "chat-generation"); err != nil {
		t.Fatalf("claim generation: %v", err)
	}
	created, err := st.AppendUserMessage(
		ctx, conversation.ID, testSession, "chat-generation",
		domain.ConversationMessage{
			ID: "expected-user", Text: "expected user", Origin: domain.MessageOriginHuman,
			ClientMessageID: "expected-client",
		},
		"expected-turn", now,
	)
	if err != nil || !created {
		t.Fatalf("append user: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "expected-turn", "expected-provider-turn", now); err != nil {
		t.Fatalf("bind turn: %v", err)
	}
	if err := st.SettleAssistantMessage(
		ctx, conversation.ID, "expected-assistant-item", "expected-provider-turn",
		"reassigned high water", "expected-assistant", now,
	); err != nil {
		t.Fatalf("settle assistant: %v", err)
	}
	if err := st.SettleTurn(
		ctx, conversation.ID, "expected-provider-turn", domain.TurnStateCompleted, "", now,
	); err != nil {
		t.Fatalf("settle turn: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "reassigned-start", ProviderTurnID: "reassigned-provider-turn"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "reassigned-user", ProviderTurnID: "reassigned-provider-turn", ProviderItemID: "reassigned-user-item", ClientMessageID: "expected-client", Text: "expected user"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "reassigned-assistant", ProviderTurnID: "reassigned-provider-turn", ProviderItemID: "expected-assistant-item", Text: "reassigned high water"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "reassigned-complete", ProviderTurnID: "reassigned-provider-turn", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns,
				Messages:     rows.Messages,
				Activities:   rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("high-water-reassigned-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	}); err != nil {
		t.Fatalf("Start with mapped reassigned provider turn: %v", err)
	}
}

func TestInterfaceHandoffProviderHistoryCannotWaiveTrustedCheckpointNativeIdentityMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		state     domain.ConversationCheckpointState
		assistant string
	}{
		{name: "pending prompt", state: domain.ConversationCheckpointPrompt},
		{name: "completed turn", state: domain.ConversationCheckpointComplete, assistant: "trusted answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openStore(t)
			rec, found, err := st.GetSession(context.Background(), testSession)
			if err != nil || !found {
				t.Fatalf("load session: found=%v err=%v", found, err)
			}
			rec.Metadata.LatestUserPrompt = "trusted prompt from the prior native thread"
			rec.Metadata.LatestAssistantUpdate = tt.assistant
			rec.Metadata.ConversationCheckpointState = tt.state
			rec.Metadata.ConversationCheckpointGeneration = "terminal-generation"
			rec.Metadata.ConversationCheckpointNativeID = "thread-before-clear"
			if err := st.UpdateSession(context.Background(), rec); err != nil {
				t.Fatalf("seed trusted checkpoint: %v", err)
			}

			conv := &nativeHistoryConversation{fakeConversation: newFakeConversation()}
			conv.providerConversationID = "thread-after-clear"
			svc := chatsvc.New(chatsvc.Options{
				Store: st, Sessions: st,
				Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
				Log:     slog.New(slog.DiscardHandler),
				NewID:   func() string { return fmt.Sprintf("native-identity-%d", time.Now().UnixNano()) },
			})
			t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

			_, err = svc.Start(context.Background(), chatsvc.StartConfig{
				SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
				WorkspacePath: t.TempDir(), ProviderConversationID: "thread-after-clear",
				HistoryMode:   ports.ChatHistoryRequired,
				HistoryPolicy: domain.SessionInterfaceTransitionHistoryProvider,
			})
			if !errors.Is(err, ports.ErrChatHistoryUnsettled) {
				t.Fatalf("Start error = %v, want hard native-identity mismatch", err)
			}
			if ports.ChatHistoryMismatchOnlyUntrustedText(err) {
				t.Fatalf("native-identity mismatch was marked recoverable: %v", err)
			}
			if dimensions := ports.ChatHistoryMismatchDimensions(err); !slices.Contains(dimensions, ports.ChatHistoryMismatchNativeIdentity) {
				t.Fatalf("mismatch dimensions = %v, want native identity", dimensions)
			}
		})
	}
}

func TestInterfaceHandoffRoundTripRetiresTrustedTerminalCheckpointAfterChatTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st := openStore(t)
	now := time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC)
	rec, found, err := st.GetSession(ctx, testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	// Terminal turn A was the trusted checkpoint used for the first TUI -> Chat
	// admission. It must not remain the text gate after Chat completes turn B and
	// hands the same native conversation back to Terminal.
	rec.Metadata.AgentSessionID = "thread-1"
	rec.Metadata.ProviderConversationID = "thread-1"
	rec.Metadata.LatestUserPrompt = "Terminal turn A"
	rec.Metadata.LatestAssistantUpdate = "Terminal answer A"
	rec.Metadata.ConversationCheckpointState = domain.ConversationCheckpointComplete
	rec.Metadata.ConversationCheckpointGeneration = "tui-generation-a"
	rec.Metadata.ConversationCheckpointNativeID = "thread-1"
	if err := st.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("seed trusted Terminal checkpoint: %v", err)
	}

	conversation, err := st.CreateConversation(
		ctx, "round-trip-conversation", domain.ConversationScopeSession,
		testProject, testSession, now,
	)
	if err != nil {
		t.Fatalf("create Chat conversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(ctx, testSession, "chat-generation-b"); err != nil {
		t.Fatalf("claim Chat generation: %v", err)
	}
	created, err := st.AppendUserMessage(
		ctx, conversation.ID, testSession, "chat-generation-b",
		domain.ConversationMessage{
			ID: "chat-user-b", Text: "Chat turn B", Origin: domain.MessageOriginHuman,
			ClientMessageID: "chat-client-b",
		},
		"chat-turn-b", now,
	)
	if err != nil || !created {
		t.Fatalf("append Chat turn B: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(ctx, "chat-turn-b", "provider-turn-b", now); err != nil {
		t.Fatalf("bind Chat turn B: %v", err)
	}
	if err := st.SettleAssistantMessage(
		ctx, conversation.ID, "provider-assistant-b", "provider-turn-b",
		"Chat answer B", "chat-assistant-b", now,
	); err != nil {
		t.Fatalf("settle Chat answer B: %v", err)
	}
	if err := st.SettleTurn(
		ctx, conversation.ID, "provider-turn-b", domain.TurnStateCompleted, "", now,
	); err != nil {
		t.Fatalf("settle Chat turn B: %v", err)
	}

	changed, err := st.CommitSessionControllerEpoch(
		ctx, testSession, domain.SessionModeChat, domain.SessionModeTUI, "thread-1", now.Add(time.Second),
	)
	if err != nil || !changed {
		t.Fatalf("commit Chat -> TUI: changed=%v err=%v", changed, err)
	}
	changed, err = st.CommitSessionControllerEpoch(
		ctx, testSession, domain.SessionModeTUI, domain.SessionModeChat, "thread-1", now.Add(2*time.Second),
	)
	if err != nil || !changed {
		t.Fatalf("commit TUI -> Chat: changed=%v err=%v", changed, err)
	}

	provider := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "provider-start-b", ProviderTurnID: "provider-turn-b"},
			{Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "provider-user-b", ProviderTurnID: "provider-turn-b", ProviderItemID: "provider-user-item-b", Text: "Chat turn B"},
			{Kind: ports.ChatEventMessageCompleted, ProviderEventID: "provider-assistant-b", ProviderTurnID: "provider-turn-b", ProviderItemID: "provider-assistant-item-b", Text: "Chat answer B"},
			{Kind: ports.ChatEventTurnCompleted, ProviderEventID: "provider-complete-b", ProviderTurnID: "provider-turn-b", TurnState: domain.TurnStateCompleted},
		},
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: provider}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("round-trip-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(ctx, chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
		HistoryPolicy: domain.SessionInterfaceTransitionHistoryStrict,
	}); err != nil {
		t.Fatalf("round-trip TUI -> Chat admission: %v", err)
	}
}

func TestInterfaceHandoffDoesNotAnchorReplayCheckpointOnFailedTurn(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 19, 3, 0, 0, 0, time.UTC)
	existing, err := st.CreateConversation(context.Background(), "failed-anchor-conversation",
		domain.ConversationScopeSession, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(context.Background(), testSession, "old-generation"); err != nil {
		t.Fatalf("ClaimChatControllerGeneration: %v", err)
	}
	// An older completed Chat round trip that the provider will replay.
	created, err := st.AppendUserMessage(context.Background(), existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "settled-user", Text: "What changed?", Origin: domain.MessageOriginHuman,
			ClientMessageID: "settled-client-id",
		}, "settled-turn", now)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage settled: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(context.Background(), "settled-turn", "native-turn-1", now); err != nil {
		t.Fatalf("BindTurnToProvider settled: %v", err)
	}
	if err := st.SettleAssistantMessage(context.Background(), existing.ID,
		"native-answer-1", "native-turn-1", "Nothing is dirty.", "settled-answer", now); err != nil {
		t.Fatalf("SettleAssistantMessage settled: %v", err)
	}
	if err := st.SettleTurn(context.Background(), existing.ID, "native-turn-1",
		domain.TurnStateCompleted, "", now); err != nil {
		t.Fatalf("SettleTurn settled: %v", err)
	}
	// A newer failed Chat turn. Its synthetic auth-error answer lives on a dead
	// transcript branch: the provider forked the next TUI prompt from the entry
	// before this turn, so session/load never replays these items.
	later := now.Add(time.Minute)
	created, err = st.AppendUserMessage(context.Background(), existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "failed-user", Text: "Spawn a worker to fix the link behavior.", Origin: domain.MessageOriginHuman,
			ClientMessageID: "failed-client-id",
		}, "failed-turn", later)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage failed turn: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(context.Background(), "failed-turn", "native-turn-2", later); err != nil {
		t.Fatalf("BindTurnToProvider failed turn: %v", err)
	}
	if err := st.SettleAssistantMessage(context.Background(), existing.ID,
		"native-error-1", "native-turn-2",
		"Failed to authenticate: OAuth session expired and could not be refreshed", "failed-answer", later); err != nil {
		t.Fatalf("SettleAssistantMessage failed turn: %v", err)
	}
	if err := st.SettleTurn(context.Background(), existing.ID, "native-turn-2",
		domain.TurnStateFailed, "authentication_failed", later); err != nil {
		t.Fatalf("SettleTurn failed turn: %v", err)
	}

	// The user re-ran the request in the terminal after fixing auth; hooks
	// captured that newest round trip, and the provider replays it.
	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Spawn a worker to fix the link behavior in the terminal."
	rec.Metadata.LatestAssistantUpdate = "Worker spawned."
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed native replay checkpoint: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start-1", ProviderTurnID: "native-turn-1"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user-1",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-1", Text: "What changed?",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer-1",
				ProviderTurnID: "native-turn-1", ProviderItemID: "history-item-2", Text: "Nothing is dirty.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-complete-1",
				ProviderTurnID: "native-turn-1", TurnState: domain.TurnStateCompleted,
			},
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "history-start-2", ProviderTurnID: "native-turn-tui"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "history-user-2",
				ProviderTurnID: "native-turn-tui", ProviderItemID: "history-item-3",
				Text: "Spawn a worker to fix the link behavior in the terminal.",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "history-answer-2",
				ProviderTurnID: "native-turn-tui", ProviderItemID: "history-item-4", Text: "Worker spawned.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "history-complete-2",
				ProviderTurnID: "native-turn-tui", TurnState: domain.TurnStateCompleted,
			},
		},
	}
	var idMu sync.Mutex
	nextID := 0
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns,
				Messages:     rows.Messages,
				Activities:   rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID: func() string {
			idMu.Lock()
			defer idMu.Unlock()
			nextID++
			return fmt.Sprintf("failed-anchor-%d", nextID)
		},
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "thread-1", HistoryMode: ports.ChatHistoryRequired,
	})
	if err != nil {
		t.Fatalf("Start resume = %v, want success: a failed turn's never-replayed items must not gate the handoff", err)
	}
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	// The failed turn stays durable in Tallpine's projection even though the provider
	// never replays it.
	var failedState domain.TurnState
	for _, turn := range snapshot.Turns {
		if turn.ProviderTurnID == "native-turn-2" {
			failedState = turn.State
		}
	}
	if failedState != domain.TurnStateFailed {
		t.Fatalf("failed turn state = %q, want preserved %q (turns = %#v)",
			failedState, domain.TurnStateFailed, snapshot.Turns)
	}
}

func TestInterfaceHandoffDoesNotAnchorReplayBeforeProviderCoordinationBoundary(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	now := time.Date(2026, 8, 23, 17, 0, 0, 0, time.UTC)
	existing, err := st.CreateConversation(context.Background(), "provider-boundary-conversation",
		domain.ConversationScopeSession, testProject, testSession, now)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if err := st.ClaimChatControllerGeneration(context.Background(), testSession, "old-generation"); err != nil {
		t.Fatalf("ClaimChatControllerGeneration: %v", err)
	}

	// This completed turn belongs to the provider that owned the session before
	// the provider handoff. The new provider receives it through hidden handoff
	// instructions, so its native thread cannot replay the old provider IDs.
	created, err := st.AppendUserMessage(context.Background(), existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID: "old-provider-user", Text: "Finish the earlier task.", Origin: domain.MessageOriginHuman,
			ClientMessageID: "old-provider-client",
		}, "old-provider-turn", now)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage old provider: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(context.Background(), "old-provider-turn", "old-provider-turn-id", now); err != nil {
		t.Fatalf("BindTurnToProvider old provider: %v", err)
	}
	if err := st.SettleAssistantMessage(context.Background(), existing.ID,
		"old-provider-answer-id", "old-provider-turn-id", "Earlier work finished.", "old-provider-answer", now); err != nil {
		t.Fatalf("SettleAssistantMessage old provider: %v", err)
	}
	if err := st.SettleTurn(context.Background(), existing.ID, "old-provider-turn-id",
		domain.TurnStateCompleted, "", now); err != nil {
		t.Fatalf("SettleTurn old provider: %v", err)
	}

	// The first native turn in the replacement provider is Tallpine's handoff marker.
	// It is a durable boundary even when the provider rejected that turn.
	boundaryAt := now.Add(time.Minute)
	created, err = st.AppendUserMessage(context.Background(), existing.ID, testSession, "old-generation",
		domain.ConversationMessage{
			ID:     "coordination-user",
			Text:   "Tallpine transferred the previous agent's context in hidden system instructions. Continue the task.",
			Origin: domain.MessageOriginDaemon, ClientMessageID: "coordination-client",
		}, "coordination-turn", boundaryAt)
	if err != nil || !created {
		t.Fatalf("AppendUserMessage coordination: created=%v err=%v", created, err)
	}
	if err := st.BindTurnToProvider(context.Background(), "coordination-turn", "new-provider-boundary", boundaryAt); err != nil {
		t.Fatalf("BindTurnToProvider coordination: %v", err)
	}
	if err := st.SettleTurn(context.Background(), existing.ID, "new-provider-boundary",
		domain.TurnStateFailed, "unsupported model", boundaryAt); err != nil {
		t.Fatalf("SettleTurn coordination: %v", err)
	}

	rec, found, err := st.GetSession(context.Background(), testSession)
	if err != nil || !found {
		t.Fatalf("load session: found=%v err=%v", found, err)
	}
	rec.Metadata.LatestUserPrompt = "Run the current provider check."
	if err := st.UpdateSession(context.Background(), rec); err != nil {
		t.Fatalf("seed native replay checkpoint: %v", err)
	}

	conv := &nativeHistoryConversation{
		fakeConversation: newFakeConversation(),
		events: []ports.ChatEvent{
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "boundary-start", ProviderTurnID: "new-provider-boundary"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "boundary-user",
				ProviderTurnID: "new-provider-boundary", ProviderItemID: "boundary-item",
				Text: "Tallpine transferred the previous agent's context in hidden system instructions. Continue the task.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "boundary-complete",
				ProviderTurnID: "new-provider-boundary", TurnState: domain.TurnStateFailed,
			},
			{Kind: ports.ChatEventTurnStarted, ProviderEventID: "current-start", ProviderTurnID: "new-provider-turn"},
			{
				Kind: ports.ChatEventUserMessageCompleted, ProviderEventID: "current-user",
				ProviderTurnID: "new-provider-turn", ProviderItemID: "current-user-item",
				Text: "Run the current provider check.",
			},
			{
				Kind: ports.ChatEventMessageCompleted, ProviderEventID: "current-answer",
				ProviderTurnID: "new-provider-turn", ProviderItemID: "current-answer-item",
				Text: "The current provider check passed.",
			},
			{
				Kind: ports.ChatEventTurnCompleted, ProviderEventID: "current-complete",
				ProviderTurnID: "new-provider-turn", TurnState: domain.TurnStateCompleted,
			},
		},
	}
	conv.providerConversationID = "new-provider-thread"
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns, Messages: rows.Messages, Activities: rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("provider-boundary-%d", time.Now().UnixNano()) },
	})
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })

	if _, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ProviderConversationID: "new-provider-thread", HistoryMode: ports.ChatHistoryRequired,
	}); err != nil {
		t.Fatalf("Start resume = %v, want success after the replacement-provider boundary", err)
	}
}

func TestSlowNativeHistoryDoesNotBlockOtherControllerLookups(t *testing.T) {
	st := openStore(t)
	conv := &blockingHistoryConversation{
		fakeConversation: newFakeConversation(),
		started:          make(chan struct{}),
		release:          make(chan struct{}),
	}
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Reader: chatsvc.SnapshotReaderFunc(func(ctx context.Context, conversationID string) (chatsvc.ConversationRows, error) {
			rows, err := st.LoadConversationSnapshot(ctx, conversationID)
			if err != nil {
				return chatsvc.ConversationRows{}, err
			}
			return chatsvc.ConversationRows{
				Conversation: rows.Conversation,
				Turns:        rows.Turns, Messages: rows.Messages, Activities: rows.Activities,
			}, nil
		}),
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return fmt.Sprintf("lock-id-%d", time.Now().UnixNano()) },
	})
	workspace := t.TempDir()
	startDone := make(chan error, 1)
	go func() {
		_, err := svc.Start(context.Background(), chatsvc.StartConfig{
			SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
			WorkspacePath: workspace, ProviderConversationID: "thread-1",
		})
		startDone <- err
	}()
	select {
	case <-conv.started:
	case <-time.After(time.Second):
		t.Fatal("native history import did not start")
	}

	lookupDone := make(chan error, 1)
	go func() {
		_, err := svc.Controller(domain.SessionID("another-session"))
		lookupDone <- err
	}()
	select {
	case err := <-lookupDone:
		if !errors.Is(err, chatsvc.ErrNoController) {
			t.Fatalf("Controller error = %v, want ErrNoController", err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("a slow native history import blocked an unrelated controller lookup")
	}

	close(conv.release)
	if err := <-startDone; err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
}
