package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestFlushQuietReportsReleasesOnlyAtThreshold(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	add := func(id string, quiet bool) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{
			ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, Quiet: quiet, Note: "n",
			State: domain.ReportCheckpoint, CreatedAt: now, DeliveryState: domain.ReportPending,
			AvailableAt: now.Add(time.Hour), RepeatCount: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	add("q1", true)
	add("q2", true)
	add("loud", false)
	if n, err := s.FlushQuietReports(ctx, sess.ProjectID, 3, now); err != nil || n != 0 {
		t.Fatalf("below threshold released %d (err %v)", n, err)
	}
	add("q3", true)
	if n, err := s.FlushQuietReports(ctx, sess.ProjectID, 3, now); err != nil || n != 3 {
		t.Fatalf("at threshold released %d (err %v), want 3", n, err)
	}
	for id, wantNow := range map[string]bool{"q1": true, "q2": true, "q3": true, "loud": false} {
		got, _, _ := s.GetReport(ctx, id)
		if got.AvailableAt.Equal(now) != wantNow {
			t.Errorf("%s available_at=%v released=%v want %v", id, got.AvailableAt, got.AvailableAt.Equal(now), wantNow)
		}
	}
}
