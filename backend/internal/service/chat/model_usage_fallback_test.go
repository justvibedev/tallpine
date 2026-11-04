package chat_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
	"tallpine.eu/tallpine/backend/internal/service/chat"
)

// Codex, DeepSeek and Kiro report cumulative conversation totals but no
// per-model turn ledger; session usage must still surface them, attributed to
// the session's model.
func TestModelUsageFallsBackToConversationTotals(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	db := fallbackTestDB(t, h.st)
	if _, err := db.Exec(`UPDATE sessions SET model = 'gpt-5.6-luna' WHERE id = ?`, string(testSession)); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatalf("ConversationForSession: %v", err)
	}
	if rows, err := h.svc.ModelUsage(ctx, testSession); err != nil || len(rows) != 0 {
		t.Fatalf("before any usage: rows=%v err=%v", rows, err)
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{
		InputTokens: 1000, CachedTokens: 400, OutputTokens: 50, TotalTokens: 1050,
	}); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	got := rows[0]
	if got.Model != "gpt-5.6-luna" || got.InputTokens != 600 || got.CacheReadTokens != 400 || got.OutputTokens != 50 {
		t.Fatalf("got %+v", got)
	}
}

// DeepSeek reports cache hit and miss as disjoint counts (total = in+cached+out),
// so fresh input must not have the cache subtracted from it.
func TestModelUsageFallbackDisjointCache(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{
		InputTokens: 100, CachedTokens: 900, OutputTokens: 50, TotalTokens: 1050,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].InputTokens != 100 || rows[0].CacheReadTokens != 900 {
		t.Fatalf("got %+v", rows[0])
	}
}

// Kiro writes a credits-only ledger row; the conversation's token totals must
// still show beside it instead of being skipped.
func TestModelUsageMergesTotalsIntoCreditsOnlyRow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.AddModelUsage(ctx, conversation.ID, []domain.ModelUsage{{Model: "qwen3-coder-next", Credits: 0.75}}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{
		InputTokens: 100, OutputTokens: 20, TotalTokens: 120,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].Credits != 0.75 || rows[0].InputTokens != 100 || rows[0].OutputTokens != 20 {
		t.Fatalf("got %+v, want credits and tokens together", rows[0])
	}
}

// Turns are counted from the conversation's finished turns for every harness,
// and a session spawned without a model reports the one chosen for the
// conversation instead of "unknown".
func TestModelUsageCountsFinishedTurnsAndResolvesModel(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	db := fallbackTestDB(t, h.st)
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetConversationSettings(ctx, conversation.ID, domain.ConversationSettings{Model: "deepseek-v4"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for i, state := range []string{"completed", "completed", "failed", "running"} {
		if _, err := db.Exec(`INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, controller_generation, state, requested_at) VALUES (?, ?, ?, 1, ?, ?)`,
			"turn-"+string(rune('a'+i)), conversation.ID, string(testSession), state, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert turn: %v", err)
		}
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	if rows[0].Model != "deepseek-v4" || rows[0].Turns != 2 {
		t.Fatalf("want model deepseek-v4 turns 2, got %+v", rows[0])
	}
}

// session get calls EffectiveModel; it must answer at once while a start, stop
// or switch holds the controller gate, not queue behind it.
func TestEffectiveModelDoesNotWaitForControllerGate(t *testing.T) {
	h := newHarness(t)
	release := chat.HoldControllerGate(h.svc, testSession)
	defer release()
	done := make(chan string, 1)
	go func() { done <- h.svc.EffectiveModel(context.Background(), testSession) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("EffectiveModel blocked behind the controller gate")
	}
}

// Credits-only rows carry their own turns; the appended totals row must count
// only the remainder.
func TestModelUsageTurnsNotDoubleCountedWithCreditsRows(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	db := fallbackTestDB(t, h.st)
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"m-a", "m-b"} {
		if err := h.st.AddModelUsage(ctx, conversation.ID, []domain.ModelUsage{{Model: m, Credits: 1}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(`INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, controller_generation, state, requested_at) VALUES (?, ?, ?, 1, 'completed', ?)`,
			"t"+string(rune('a'+i)), conversation.ID, string(testSession), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.Turns
	}
	if sum != 2 {
		t.Fatalf("turns summed over rows = %d, want 2: %+v", sum, rows)
	}
}

// A session with no pinned or chosen model runs the provider's default; the
// live provider reports it as the model option's current value.
func TestEffectiveModelReadsLiveProviderDefault(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	conv := &configOptionConversation{
		fakeConversation: newFakeConversation(),
		applied:          []ports.ChatConfigOptionValue{{Select: "provider-default"}},
	}
	svc := chat.New(chat.Options{
		Store: st, Sessions: st,
		Drivers: fakeRegistry{driver: fakeDriver{conv: conv}},
		Log:     slog.New(slog.DiscardHandler),
		NewID:   func() string { return "effective-model-id" },
	})
	t.Cleanup(func() { _ = svc.Stop(ctx, testSession) })
	if _, err := svc.Start(ctx, chat.StartConfig{
		SessionID: testSession, ProjectID: testProject, Harness: domain.HarnessCodex,
		WorkspacePath: t.TempDir(),
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := svc.EffectiveModel(ctx, testSession); got != "provider-default" {
		t.Fatalf("EffectiveModel = %q, want provider-default", got)
	}
}

// The merge branch (the resolved model has a credits-only row) must not
// double-count either: with several such rows the column still sums to the
// conversation's finished turns.
func TestModelUsageTurnsNotDoubleCountedWhenMergingIntoCreditsRow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	db := fallbackTestDB(t, h.st)
	if _, err := db.Exec(`UPDATE sessions SET model = 'm-a' WHERE id = ?`, string(testSession)); err != nil {
		t.Fatal(err)
	}
	conversation, err := h.st.ConversationForSession(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"m-a", "m-b"} {
		if err := h.st.AddModelUsage(ctx, conversation.ID, []domain.ModelUsage{{Model: m, Credits: 1}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(`INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, controller_generation, state, requested_at) VALUES (?, ?, ?, 1, 'completed', ?)`,
			"u"+string(rune('a'+i)), conversation.ID, string(testSession), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.RecordUsage(ctx, conversation.ID, domain.ConversationUsage{InputTokens: 10, OutputTokens: 1, TotalTokens: 11}); err != nil {
		t.Fatal(err)
	}
	rows, err := h.svc.ModelUsage(ctx, testSession)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, r := range rows {
		sum += r.Turns
	}
	if sum != 2 {
		t.Fatalf("turns summed over rows = %d, want 2: %+v", sum, rows)
	}
}
