package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// B85 measurement: a queued message that joins another turn's input records how
// it was delivered, and requested_at/completed_at on the same row give the
// queued-to-delivered delay.
func TestQueuedBatchRecordsDeliveryModeAndDelay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	seedProject(t, s, "hist")
	rec := sampleRecord("hist")
	rec.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatal(err)
	}
	conv, err := s.CreateConversation(ctx, "conv-mode", domain.ConversationScopeSession, "hist", session.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, session.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	add := func(id string, at time.Duration) {
		t.Helper()
		if _, err := s.AppendUserMessage(ctx, conv.ID, session.ID, "gen-1", domain.ConversationMessage{
			ID: "m-" + id, Text: id, ClientMessageID: id, Origin: domain.MessageOriginHuman,
		}, id, histClock.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	add("running", 0)
	if err := s.BindTurnToProvider(ctx, "running", "p1", histClock); err != nil {
		t.Fatal(err)
	}
	add("waited", time.Second)
	for _, mode := range []string{domain.DeliveryModeBatched, domain.DeliveryModeSteered} {
		batch, err := s.ClaimQueuedBatch(ctx, conv.ID, false, 0, histClock.Add(time.Minute))
		if err != nil || len(batch) != 1 {
			t.Fatalf("claim %s: %+v %v", mode, batch, err)
		}
		if err := s.CompleteQueuedBatch(ctx, conv.ID, []string{batch[0].TurnID}, "p1", mode, histClock.Add(90*time.Second)); err != nil {
			t.Fatal(err)
		}
		var gotMode string
		var requested, completed time.Time
		if err := raw.QueryRow(`SELECT delivery_mode, requested_at, completed_at
			FROM conversation_turns WHERE id = ?`, batch[0].TurnID).Scan(&gotMode, &requested, &completed); err != nil {
			t.Fatal(err)
		}
		if delay := completed.Sub(requested); gotMode != mode || delay < 88*time.Second || delay > 90*time.Second {
			t.Fatalf("%s: delivery_mode=%q delay=%s", mode, gotMode, delay)
		}
		add(fmt.Sprint("next-", mode), 2*time.Second) // one more waiter for the next mode
	}
}
