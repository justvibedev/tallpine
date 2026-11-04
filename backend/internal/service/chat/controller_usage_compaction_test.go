package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	chatsvc "tallpine.eu/tallpine/backend/internal/service/chat"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

// Usage is current state, not history. The provider reports it after every tool
// call, so the projection must overwrite: a row per report is what buried the
// conversation, and the conversation is only ever one amount full.
func TestUsageProjectionKeepsOnlyTheLatest(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
			ContextUsed: 18055, ContextWindow: 258400,
			InputTokens: 18050, OutputTokens: 5, CachedTokens: 11008, TotalTokens: 18055,
		}},
		ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
			ContextUsed: 42100, ContextWindow: 258400,
			InputTokens: 41000, OutputTokens: 1100, CachedTokens: 20000, TotalTokens: 60155,
		}},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.ContextUsed == 42100
	})

	usage := snapshot.Conversation.Usage
	if usage.ContextWindow != 258400 {
		t.Errorf("context window = %d, want 258400", usage.ContextWindow)
	}
	if usage.TotalTokens != 60155 {
		t.Errorf("cumulative total = %d, want the later 60155", usage.TotalTokens)
	}
	// The readout is only meaningful as a fraction; without the window it is a
	// number with no scale, which is what the header used to show.
	if got := usage.ContextFraction(); got < 0.16 || got > 0.17 {
		t.Errorf("context fraction = %v, want roughly 0.163", got)
	}

	// Usage must not become a timeline entry, under any kind.
	for _, activity := range snapshot.Activities {
		if activity.Kind == domain.ActivityKindUsage {
			t.Fatalf("usage was projected as an activity: %+v", activity)
		}
	}
}

// ACP reports context fullness and cumulative token totals in separate messages.
// A later totals update must not erase the context window received just before it.
func TestUsageProjectionMergesIndependentProviderUpdates(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
			ContextUsed: 74300, ContextWindow: 1_000_000, ContextKnown: true,
		}},
		ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
			InputTokens: 2, OutputTokens: 59, CachedTokens: 74216,
			TotalTokens: 74277, TotalsKnown: true,
		}},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.TotalTokens == 74277
	})
	usage := snapshot.Conversation.Usage
	if usage.ContextUsed != 74300 || usage.ContextWindow != 1_000_000 {
		t.Fatalf("context usage was erased by totals update: %+v", usage)
	}
	if usage.CachedTokens != 74216 {
		t.Fatalf("cumulative totals were not merged: %+v", usage)
	}
}

// A model the provider states no window for still reports its tokens. The meter
// has to say "unknown" rather than draw an empty bar for a conversation that may
// be nearly full.
func TestUsageProjectionWithoutContextWindow(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		ContextUsed: 900, TotalTokens: 900, InputTokens: 800, OutputTokens: 100,
	}})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil
	})
	if got := snapshot.Conversation.Usage.ContextFraction(); got != -1 {
		t.Errorf("context fraction = %v, want -1 for an unknown window", got)
	}
}

// Rate limits are current state too, and an unreported window must survive a round
// trip through the database as unreported rather than as a reassuring zero.
func TestRateLimitProjectionKeepsOnlyTheLatest(t *testing.T) {
	t.Parallel()

	h := newHarnessForHarness(t, domain.HarnessClaudeCode)

	h.conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventRateLimits, RateLimits: &ports.ChatRateLimits{
			PrimaryUsedPercent: 12, SecondaryUsedPercent: -1,
			PrimaryResetsInSeconds: 600, PlanLabel: "pro",
		}},
		ports.ChatEvent{Kind: ports.ChatEventRateLimits, RateLimits: &ports.ChatRateLimits{
			PrimaryUsedPercent: 71, SecondaryUsedPercent: -1,
			PrimaryResetsInSeconds: 490444, PlanLabel: "pro",
		}},
	)

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.RateLimits != nil && s.Conversation.RateLimits.PrimaryUsedPercent == 71
	})

	limits := snapshot.Conversation.RateLimits
	if limits.SecondaryUsedPercent >= 0 {
		t.Errorf("secondary = %v; an unreported window must not read as untouched quota",
			limits.SecondaryUsedPercent)
	}
	if limits.PrimaryResetsInSeconds != 490444 {
		t.Errorf("primary resets in %d, want 490444", limits.PrimaryResetsInSeconds)
	}
	if limits.PlanLabel != "pro" {
		t.Errorf("plan = %q, want pro", limits.PlanLabel)
	}
	if got := limits.WorstUsedPercent(); got != 71 {
		t.Errorf("worst window = %v, want 71", got)
	}
}

func TestCodexRateLimitsUpdateActiveAccountCapacityWithoutConversationPersistence(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	conv := newFakeConversation()
	updates := make(chan ports.CodexCapacityObservation, 1)
	svc := chatsvc.New(chatsvc.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "bound-capacity" },
		OnCodexCapacityChanged: func(sessionID domain.SessionID, generation string, observation ports.CodexCapacityObservation) {
			if sessionID != testSession || generation != "managed-generation" {
				t.Errorf("capacity attribution = %s/%s", sessionID, generation)
			}
			updates <- observation
		},
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ControllerGeneration: "managed-generation",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	observation := ports.CodexCapacityObservation{Partial: true, Overall: &domain.CodexCapacityBucket{
		LimitID: "codex", Reached: domain.CodexCapacityNotReached,
		Primary: &domain.CodexCapacityWindow{UsedPercent: 81},
	}}
	conv.emit(ports.ChatEvent{Kind: ports.ChatEventRateLimits, ProviderEventID: "capacity-1", RateLimits: &ports.ChatRateLimits{
		PrimaryUsedPercent: 81, PlanLabel: "pro", CodexCapacity: &observation,
	}})
	select {
	case got := <-updates:
		if got.Overall == nil || got.Overall.Primary == nil || got.Overall.Primary.UsedPercent != 81 {
			t.Fatalf("capacity callback = %#v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for bound profile capacity update")
	}
	// The callback runs only after the archive row commits, so it is a barrier
	// for the reads below.
	snapshot, err := st.LoadConversationSnapshot(context.Background(), ctrl.ConversationID())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Conversation.RateLimits != nil {
		t.Fatalf("bound Codex rate limits persisted: %#v", snapshot.Conversation.RateLimits)
	}
	events, err := st.ProviderEventsSince(context.Background(), ctrl.ConversationID(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var archived map[string]any
	if len(events) != 1 || json.Unmarshal([]byte(events[0].PayloadJson), &archived) != nil || archived["rateLimits"] != nil || strings.Contains(events[0].PayloadJson, "usedPercent") {
		t.Fatalf("bound Codex capacity leaked into provider archive: %#v", events)
	}
}

// A capacity observation whose archive row rolls back was never recorded, so the
// daemon's account state must not hear about it; the provider's replay will.
func TestCodexCapacityIsNotPublishedWhenItsArchiveRollsBack(t *testing.T) {
	st := openStore(t)
	conv := newFakeConversation()
	failing := &failingProjectStore{
		Store: st, failMethod: string(ports.ChatEventRateLimits), failed: make(chan struct{}),
	}
	var published atomic.Int32
	svc := chatsvc.New(chatsvc.Options{
		Store: failing, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "rolled-back-capacity" },
		OnCodexCapacityChanged: func(domain.SessionID, string, ports.CodexCapacityObservation) {
			published.Add(1)
		},
	})
	ctrl, err := svc.Start(context.Background(), chatsvc.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(), ControllerGeneration: "managed-generation",
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background(), testSession) })
	observation := ports.CodexCapacityObservation{Overall: &domain.CodexCapacityBucket{
		LimitID: "codex", Primary: &domain.CodexCapacityWindow{UsedPercent: 81},
	}}
	conv.emit(
		ports.ChatEvent{Kind: ports.ChatEventRateLimits, ProviderEventID: "capacity-1", RateLimits: &ports.ChatRateLimits{
			CodexCapacity: &observation,
		}},
		// A later event that commits proves the rolled-back one was fully handled.
		ports.ChatEvent{Kind: ports.ChatEventThreadRenamed, ProviderEventID: "title-1", Title: "after"},
	)
	<-failing.failed
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		events, err := st.ProviderEventsSince(context.Background(), ctrl.ConversationID(), 0, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("archive = %d events, want only the committed rename", len(events))
		}
	}
	if n := published.Load(); n != 0 {
		t.Fatalf("capacity published %d times for an archive row that rolled back", n)
	}
}

// Nothing reported yet is distinct from a conversation using nothing: the snapshot
// leaves both nil so a client can withhold the meter rather than draw an empty one.
func TestSnapshotOmitsUsageUntilTheProviderReports(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snapshot.Conversation.Usage != nil {
		t.Errorf("usage = %+v, want nil before the provider reports", snapshot.Conversation.Usage)
	}
	if snapshot.Conversation.RateLimits != nil {
		t.Errorf("rate limits = %+v, want nil before the provider reports",
			snapshot.Conversation.RateLimits)
	}
}

/* ---- compaction --------------------------------------------------------- */

// A compaction has to survive a restart, which means it has to be a row. Without
// one, a conversation that quietly lost half its history has nothing in the
// timeline to explain the gap, and reads as if the agent simply forgot.
func TestCompactionIsProjectedAsATimelineFact(t *testing.T) {
	t.Parallel()

	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)

	conv.emit(ports.ChatEvent{
		Kind:           ports.ChatEventCompacted,
		ProviderTurnID: "compact-turn",
		ProviderItemID: "cc-1",
		Summary:        "Compacted history, freeing 11.0k tokens",
		Detail:         []byte(`{"tokensBefore":15650,"tokensAfter":4632,"tokensReclaimed":11018}`),
	})

	// Both writes, because the row and the conversation flag are two statements and
	// this test asserts on both: waiting only on the row races the flag.
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Activities) == 1 && s.Conversation.CompactedAt != nil
	})
	activity := snapshot.Activities[0]
	if activity.Kind != domain.ActivityKindSystem {
		t.Errorf("kind = %q, want system", activity.Kind)
	}
	if activity.Status != domain.ActivityStatusCompleted {
		t.Errorf("status = %q, want completed", activity.Status)
	}
	if activity.Summary != "Compacted history, freeing 11.0k tokens" {
		t.Errorf("summary = %q", activity.Summary)
	}
	if activity.ProviderItemID != "cc-1" {
		t.Errorf("provider item id = %q, want cc-1 so a replay updates this row", activity.ProviderItemID)
	}
	// Not attached to a turn: the provider ran the compaction in a turn Tallpine never
	// dispatched, so filing the row under it would attribute the entry to work the
	// user never asked for.
	if activity.TurnID != "" {
		t.Errorf("turn id = %q, want none", activity.TurnID)
	}
	var detail struct{ TokensReclaimed int64 }
	if err := json.Unmarshal(activity.Detail, &detail); err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.TokensReclaimed != 11018 {
		t.Errorf("reclaimed = %d, want 11018", detail.TokensReclaimed)
	}

	// The conversation itself records that compaction has run, so a client does not
	// have to scan an unbounded timeline to find out.
	if snapshot.Conversation.CompactedAt == nil {
		t.Error("conversation was not marked compacted")
	}
}

// A compaction replayed across a reconnect updates the row it already has. Two
// entries for one compaction would read as two, and the reclaim would look twice
// as large as it was.
func TestCompactionReplayDoesNotDuplicateTheRow(t *testing.T) {
	t.Parallel()

	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)

	for range 2 {
		conv.emit(ports.ChatEvent{
			Kind:           ports.ChatEventCompacted,
			ProviderItemID: "cc-1",
			Summary:        "Compacted the conversation history",
		})
	}
	// A marker after both, so this waits on an event rather than on a timeout.
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventActivityCompleted, ProviderItemID: "exec-1",
		ActivityKind: domain.ActivityKindCommand, ActivityStatus: domain.ActivityStatusCompleted,
		Summary: "date -u",
	})

	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		for _, activity := range s.Activities {
			if activity.ProviderItemID == "exec-1" {
				return true
			}
		}
		return false
	})
	compactions := 0
	for _, activity := range snapshot.Activities {
		if activity.Kind == domain.ActivityKindSystem {
			compactions++
		}
	}
	if compactions != 1 {
		t.Fatalf("got %d compaction rows for one compaction", compactions)
	}
}

func TestCompactReportsWhatIsAboutToBeReclaimed(t *testing.T) {
	t.Parallel()

	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)

	result, err := h.svc.Compact(context.Background(), testSession)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if result.TokensBefore != 15650 {
		t.Errorf("tokensBefore = %d, want 15650", result.TokensBefore)
	}
	// Zero means "not yet known", not "reclaimed everything". The settled figures
	// reach the client on the timeline.
	if result.TokensAfter != 0 {
		t.Errorf("tokensAfter = %d, want 0 while the reclaim is in flight", result.TokensAfter)
	}
	if conv.compactCalls() != 1 {
		t.Errorf("provider called %d times, want 1", conv.compactCalls())
	}
}

func TestCompactionSettlementWithoutStartDrainsQueue(t *testing.T) {
	t.Parallel()

	for _, kind := range []ports.ChatEventKind{ports.ChatEventTurnCompleted, ports.ChatEventCompacted} {
		t.Run(string(kind), func(t *testing.T) {
			conv := newCompactingConversation()
			h := newHarnessWithConversation(t, conv)
			ctx := context.Background()
			if _, err := h.svc.Compact(ctx, testSession); err != nil {
				t.Fatal(err)
			}
			if turn, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "after compact"}); err != nil || turn.State != domain.TurnStateQueued {
				t.Fatalf("Send while compacting = %+v, %v", turn, err)
			}
			conv.emit(ports.ChatEvent{Kind: kind, ProviderTurnID: "compact-turn", TurnState: domain.TurnStateCompleted})
			h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
				return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateRunning
			})
			if got := conv.sentTexts(); len(got) != 1 || got[0] != "after compact" {
				t.Fatalf("provider messages = %v", got)
			}
		})
	}
}

// A provider with no way to reclaim context gets a typed answer, so the client can
// stop offering the control instead of surfacing an internal failure the user
// cannot act on. The plain fake conversation does not implement ChatCompactor.
func TestCompactOnAProviderThatCannotIsTyped(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	_, err := h.svc.Compact(context.Background(), testSession)
	if !errors.Is(err, chatsvc.ErrCompactionUnsupported) {
		t.Fatalf("err = %v, want ErrCompactionUnsupported", err)
	}
}

// An agent might implement ChatCompactor statically (e.g. ACP conversation),
// but if the agent has not advertised the capability, Compact must return ErrCompactionUnsupported.
func TestCompactRefusesWhenProviderImplementsCompactorWithoutCapability(t *testing.T) {
	t.Parallel()

	conv := newCompactingConversation()
	caps := productionCaps()
	delete(caps, ports.ChatCapabilityCompaction)
	conv.setCapabilities(caps)
	h := newHarnessWithConversation(t, conv)

	_, err := h.svc.Compact(context.Background(), testSession)
	if !errors.Is(err, chatsvc.ErrCompactionUnsupported) {
		t.Fatalf("err = %v, want ErrCompactionUnsupported", err)
	}
}

// Measured twice against a live app-server: thread/compact/start mid-turn silently
// interrupts the running turn and reports it as interrupted, then compacts. Losing
// work the user is waiting on as a side effect of housekeeping is not something to
// discover afterwards from the timeline, so Tallpine refuses and makes them stop it.
func TestCompactRefusesWhileATurnIsInFlight(t *testing.T) {
	t.Parallel()

	conv := newCompactingConversation()
	h := newHarnessWithConversation(t, conv)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "start something long", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := h.svc.Compact(ctx, testSession); !errors.Is(err, chatsvc.ErrCompactionWhileBusy) {
		t.Fatalf("err = %v, want ErrCompactionWhileBusy", err)
	}
	if conv.compactCalls() != 0 {
		t.Error("the provider was asked to compact anyway; the running turn would have been discarded")
	}

	// Once the turn settles it is allowed again.
	conv.emit(ports.ChatEvent{
		Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1",
		TurnState: domain.TurnStateCompleted,
	})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return len(s.Turns) == 1 && s.Turns[0].State == domain.TurnStateCompleted
	})
	if _, err := h.svc.Compact(ctx, testSession); err != nil {
		t.Fatalf("Compact after the turn settled: %v", err)
	}
}

// Per-turn model usage is an increment: two turns add up, and the current-position
// totals the same events carry no part of are left alone.
func TestModelUsageEventsAccumulateIntoSessionTotals(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	turn := func(in int64) ports.ChatEvent {
		return ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{ModelUsage: []domain.ModelUsage{
			{Model: "claude-sonnet-5-5", InputTokens: in, CacheReadTokens: 100, OutputTokens: 1},
		}}}
	}
	h.conv.emit(turn(5), turn(7))
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := h.st.ModelUsageForSession(context.Background(), testSession)
		if err == nil && len(got) == 1 && got[0].Turns == 2 {
			if got[0].InputTokens != 12 || got[0].CacheReadTokens != 200 || got[0].OutputTokens != 2 {
				t.Fatalf("totals = %+v", got[0])
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("model usage never accumulated: %+v err=%v", got, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A provider that can no longer price its spend (CostUnknown) must clear the
// figure it reported earlier, not leave a stale understated one on display.
func TestUsageProjectionClearsCostWhenProviderWithdrawsIt(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cost := 0.42
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		InputTokens: 10, TotalTokens: 10, TotalsKnown: true, Cost: &cost, Currency: "USD",
	}})
	h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.Cost != nil
	})
	h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventUsage, Usage: &ports.ChatUsage{
		InputTokens: 25, TotalTokens: 25, TotalsKnown: true, CostUnknown: true,
	}})
	snapshot := h.awaitSnapshot(t, func(s store.ConversationSnapshot) bool {
		return s.Conversation.Usage != nil && s.Conversation.Usage.TotalTokens == 25
	})
	if usage := snapshot.Conversation.Usage; usage.Cost != nil || usage.Currency != "" {
		t.Fatalf("stale cost kept: %+v", usage)
	}
}
