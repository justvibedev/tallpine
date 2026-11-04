package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestLatestNeedsInputReportsReturnsTheNewestPerSession(t *testing.T) {
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
	for i, rep := range []domain.ReportRecord{
		{ID: "r1", State: domain.ReportNeedsInput, Note: "first", CreatedAt: now},
		{ID: "r2", State: domain.ReportNeedsInput, Note: "second", CreatedAt: now.Add(time.Minute)},
		{ID: "r3", State: domain.ReportCheckpoint, Note: "later but not a request", CreatedAt: now.Add(2 * time.Minute)},
	} {
		rep.SessionID, rep.ProjectID, rep.DeliveryState, rep.AvailableAt, rep.RepeatCount = sess.ID, sess.ProjectID, domain.ReportPending, now, 1
		if _, err := s.CreateReport(ctx, rep); err != nil {
			t.Fatalf("report %d: %v", i, err)
		}
	}
	got, err := s.LatestNeedsInputReports(ctx, []domain.SessionID{sess.ID, "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[sess.ID].Note != "second" || !got[sess.ID].CreatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("got %+v, want only the newest needs_input report", got)
	}
}

func TestLatestNeedsInputReportsIgnoresARequestAnsweredByALaterDoneOrStuck(t *testing.T) {
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
	file := func(id string, state domain.ReportState, at time.Time) {
		t.Helper()
		rep := domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, State: state, Note: "n", CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1}
		if state == domain.ReportDone {
			rep.SettlementDeadline = at.Add(time.Minute)
		}
		if _, err := s.CreateReport(ctx, rep); err != nil {
			t.Fatalf("report %s: %v", id, err)
		}
	}
	file("r1", domain.ReportNeedsInput, now)
	if got, _ := s.LatestNeedsInputReports(ctx, []domain.SessionID{sess.ID}); len(got) != 1 {
		t.Fatalf("open request: got %+v", got)
	}
	file("r2", domain.ReportDone, now.Add(time.Minute))
	if got, _ := s.LatestNeedsInputReports(ctx, []domain.SessionID{sess.ID}); len(got) != 0 {
		t.Fatalf("answered by a later done: got %+v, want none", got)
	}
	file("r3", domain.ReportNeedsInput, now.Add(2*time.Minute))
	if got, _ := s.LatestNeedsInputReports(ctx, []domain.SessionID{sess.ID}); got[sess.ID].ID != "r3" {
		t.Fatalf("a newer request reopens it: got %+v", got)
	}
}

// A daemon restart must neither resend an escalation (its source key dedupes it)
// nor let boot reconciliation resolve it (its worker is idle by design).
func TestNeedsInputEscalationNotificationSurvivesRestartReconcile(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seedProject(t, s, "mer")
	sess, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.NotificationRecord{
		ID: "ntf_1", SessionID: sess.ID, ProjectID: sess.ProjectID, Type: domain.NotificationNeedsInput, Title: "t",
		Status: domain.NotificationUnread, CreatedAt: now, SourceKey: domain.NeedsInputEscalationSourceKeyPrefix + "rpt_1",
	}
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || !inserted {
		t.Fatalf("first escalation: inserted=%v err=%v", inserted, err)
	}
	rec.ID = "ntf_2"
	if _, inserted, err := s.CreateNotification(ctx, rec); err != nil || inserted {
		t.Fatalf("resent escalation: inserted=%v err=%v, want a no-op", inserted, err)
	}
	resolved, err := s.ReconcileResolvedNotifications(ctx, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range resolved {
		if r.ID == "ntf_1" {
			t.Fatalf("boot reconcile resolved the escalation of an idle worker")
		}
	}
	// Killing the worker leaves nothing to wait on: the next reconcile closes it.
	sess.IsTerminated = true
	if err := s.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	resolved, err = s.ReconcileResolvedNotifications(ctx, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range resolved {
		found = found || r.ID == "ntf_1"
	}
	if !found {
		t.Fatalf("boot reconcile left the escalation of a terminated worker open: %+v", resolved)
	}
}
