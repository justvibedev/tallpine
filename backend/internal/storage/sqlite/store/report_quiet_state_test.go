package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestReportStoreQuietReportState(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, at time.Time, quiet bool) {
		t.Helper()
		rec := domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportCheckpoint, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1, Quiet: quiet}
		if strings.HasPrefix(id, "auto") {
			rec.State, rec.Note, rec.Synthesized = "", "", true
			rec.Message = "[auto] turn_ended: " + id
		}
		if _, err := s.CreateReport(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	mk("q1", now, true)
	if q, sup, err := s.QuietReportState(ctx, "q1"); err != nil || !q || sup {
		t.Fatalf("lone quiet = %v %v %v", q, sup, err)
	}
	// B459: the daemon's turn_ended notice right after a worker checkpoint
	// must not supersede (and so drop) the worker's own report.
	mk("auto1", now.Add(4*time.Second), true)
	if q, sup, _ := s.QuietReportState(ctx, "q1"); !q || sup {
		t.Fatalf("quiet worker report superseded by synthesized notice = %v %v", q, sup)
	}
	mk("n1", now.Add(time.Minute), false)
	if q, sup, _ := s.QuietReportState(ctx, "q1"); !q || !sup {
		t.Fatalf("quiet with newer = %v %v", q, sup)
	}
	if q, sup, _ := s.QuietReportState(ctx, "auto1"); !q || !sup {
		t.Fatalf("synthesized notice with newer worker report = %v %v", q, sup)
	}
	if q, sup, _ := s.QuietReportState(ctx, "n1"); q || sup {
		t.Fatalf("non-quiet newest = %v %v", q, sup)
	}
}
