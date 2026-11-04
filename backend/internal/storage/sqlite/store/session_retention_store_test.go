package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/store"
)

func countRows(t *testing.T, s *sqlite.Store, query string, args ...any) int64 {
	t.Helper()
	n, err := s.CountForTest(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// keptTables are what retention must never touch: the A/B, usage, cost and
// review analysis reads them.
var keptQueries = []string{
	"SELECT COUNT(*) FROM sessions",
	"SELECT COUNT(*) FROM conversations",
	"SELECT COUNT(*) FROM conversation_turns",
	"SELECT COUNT(*) FROM conversation_activities WHERE kind = 'system'",
	"SELECT COUNT(*) FROM usage_bindings",
	"SELECT COUNT(*) FROM model_usage_events",
	"SELECT COUNT(*) FROM conversation_model_usage",
	"SELECT COUNT(*) FROM review_run",
	"SELECT COUNT(*) FROM pr",
	"SELECT COUNT(*) FROM app_settings",
}

func TestRetentionPurgeKeepsRetainedDataAndOtherSessions(t *testing.T) {
	s, session, conversation := conversationFixture(t)
	ctx := context.Background()
	streamTurn(t, s, conversation, session, "t1", true)
	if err := s.ExecForTest(ctx, `INSERT INTO conversation_activities
		(id, conversation_id, turn_id, sequence, revision, kind, status, summary, created_at, updated_at)
		VALUES ('compaction-1', ?, NULL, 999, 1, 'system', 'completed', 'Context compacted', ?, ?)`,
		conversation, histClock, histClock); err != nil {
		t.Fatalf("seed compaction marker: %v", err)
	}

	otherRec := sampleRecord("hist")
	otherRec.Mode = domain.SessionModeChat
	other, err := s.CreateSession(ctx, otherRec)
	if err != nil {
		t.Fatal(err)
	}
	otherConv, err := s.CreateConversation(ctx, "conv-other", domain.ConversationScopeSession, "hist", other.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, other.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	streamTurn(t, s, otherConv.ID, other.ID, "o1", true)

	kept := map[string]int64{}
	for _, q := range keptQueries {
		kept[q] = countRows(t, s, q)
	}
	otherEvents := countRows(t, s, "SELECT COUNT(*) FROM conversation_provider_events WHERE session_id = ?", other.ID)
	otherMessages := countRows(t, s, "SELECT COUNT(*) FROM conversation_messages WHERE conversation_id = ?", otherConv.ID)
	if otherEvents == 0 || otherMessages == 0 {
		t.Fatalf("fixture: the other session has %d events, %d messages", otherEvents, otherMessages)
	}

	foot, err := s.SessionRetentionFootprint(ctx, session)
	if err != nil {
		t.Fatal(err)
	}
	if foot.ProviderEvents == 0 || foot.ProviderEventBytes == 0 || foot.TranscriptRows == 0 || foot.TranscriptRowsBytes == 0 {
		t.Fatalf("footprint = %+v, want events and transcript", foot)
	}
	if purged, _ := s.SessionRetentionPurged(ctx, session); purged {
		t.Fatal("session reported purged before any purge")
	}

	if n, err := s.PurgeSessionProviderEvents(ctx, session); err != nil || n != foot.ProviderEvents {
		t.Fatalf("purged %d events (err %v), want %d", n, err, foot.ProviderEvents)
	}
	if n, err := s.PurgeSessionTranscript(ctx, session); err != nil || n != foot.TranscriptRows {
		t.Fatalf("purged %d transcript rows (err %v), want %d", n, err, foot.TranscriptRows)
	}
	if err := s.MarkSessionRetentionPurge(ctx, session, true, true, time.Now()); err != nil {
		t.Fatal(err)
	}

	if after, err := s.SessionRetentionFootprint(ctx, session); err != nil || after != (store.RetentionFootprint{}) {
		t.Fatalf("footprint after purge = %+v err=%v, want empty", after, err)
	}
	if purged, err := s.SessionRetentionPurged(ctx, session); err != nil || !purged {
		t.Fatalf("purged = %v err=%v, want true", purged, err)
	}
	for _, q := range keptQueries {
		if got := countRows(t, s, q); got != kept[q] {
			t.Errorf("%s = %d after purge, want %d", q, got, kept[q])
		}
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM conversation_activities WHERE id = 'compaction-1'"); got != 1 {
		t.Error("the compaction marker was purged")
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM conversation_provider_events WHERE session_id = ?", other.ID); got != otherEvents {
		t.Errorf("other session's events = %d, want %d", got, otherEvents)
	}
	if got := countRows(t, s, "SELECT COUNT(*) FROM conversation_messages WHERE conversation_id = ?", otherConv.ID); got != otherMessages {
		t.Errorf("other session's messages = %d, want %d", got, otherMessages)
	}
}
