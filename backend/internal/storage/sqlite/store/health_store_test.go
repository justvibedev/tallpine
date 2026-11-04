package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	sqlitestore "tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestListChatSessionsExpectedLive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	chat := sampleRecord("mer")
	chat.Mode = domain.SessionModeChat
	chat.Metadata.ProviderConversationID = "conv-1"
	live, err := s.CreateSession(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}
	fresh := chat
	fresh.Metadata.ProviderConversationID = ""
	if _, err := s.CreateSession(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	tui := sampleRecord("mer")
	if _, err := s.CreateSession(ctx, tui); err != nil {
		t.Fatal(err)
	}

	got, err := s.ListChatSessionsExpectedLive(ctx, time.Now().Add(time.Hour))
	if err != nil || len(got) != 1 || got[0] != live.ID {
		t.Fatalf("got %v err %v, want only the established chat session %s", got, err, live.ID)
	}
	if got, _ := s.ListChatSessionsExpectedLive(ctx, time.Now().Add(-time.Hour)); len(got) != 0 {
		t.Fatalf("a session still inside the start-up grace must not be listed, got %v", got)
	}
}

func TestListStuckReportBatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, available time.Time) {
		t.Helper()
		if _, err := s.CreateReport(ctx, domain.ReportRecord{
			ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, CreatedAt: now,
			DeliveryState: domain.ReportPending, AvailableAt: available, RepeatCount: 1,
			Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: id}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	mk("overdue", now.Add(-time.Hour))
	mk("claimed", now.Add(-time.Hour))
	mk("future", now.Add(time.Hour))
	if _, ok, err := s.ClaimReport(ctx, "claimed", "tok", now.Add(-30*time.Minute)); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}

	got, err := s.ListStuckReportBatches(ctx, now.Add(-time.Minute))
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v err %v, want one project batch", got, err)
	}
	if b := got[0]; b.ProjectID != sess.ProjectID || b.Pending != 1 || b.Claimed != 1 || b.Since.IsZero() {
		t.Fatalf("batch = %+v, want pending=1 claimed=1", b)
	}
	if got, _ := s.ListStuckReportBatches(ctx, now.Add(-2*time.Hour)); len(got) != 0 {
		t.Fatalf("reports not yet due must not be stuck, got %+v", got)
	}
}

func TestListOverdueTurnFallbacks(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlitestore.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	now := time.Now().UTC().Truncate(time.Second)
	ins := func(id, disposition string, due time.Time) {
		t.Helper()
		if _, err := raw.Exec(`INSERT INTO turn_fallbacks (id, session_id, mode, generation, turn_id, disposition, completed_at, due_at)
VALUES (?, ?, 'tui', 'g', ?, ?, ?, ?)`, id, string(sess.ID), id, disposition, due, due); err != nil {
			t.Fatal(err)
		}
	}
	ins("late", "pending", now.Add(-time.Hour))
	ins("early", "pending", now.Add(time.Hour))
	ins("done", "emitted", now.Add(-time.Hour))

	got, err := s.ListOverdueTurnFallbacks(ctx, now)
	if err != nil || len(got) != 1 || got[0].ID != "late" || got[0].SessionID != sess.ID {
		t.Fatalf("got %+v err %v, want only the pending past-due fallback", got, err)
	}
}
