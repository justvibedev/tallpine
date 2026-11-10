package store_test

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// A newer report must not overtake an older retry batch that is backing off
// (B647: worker 1092's 21:39 report arrived after its 21:42 one).
func TestReportStoreClaimKeepsAuthoredOrderBehindBackedOffRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	create := func(id string, state domain.ReportState, at time.Time) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: state, Message: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("older", "", t0)
	if got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0); err != nil || len(got) != 1 {
		t.Fatalf("first claim %v %v", got, err)
	}
	if _, err := s.DeferReportBatch(ctx, "tp", "t1", t0.Add(30*time.Second), domain.ReportDeliveryBusyReason); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(5 * time.Second)
	create("newer", "", later)

	// The newer report is due but waits for the older batch's retry time.
	if got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t2", later); err != nil || len(got) != 0 {
		t.Fatalf("newer overtook the older batch: %v %v", got, err)
	}
	sched, err := s.ListPendingReportSchedule(ctx, 10)
	if err != nil || len(sched) != 1 || !sched[0].AvailableAt.Equal(t0.Add(30*time.Second)) {
		t.Fatalf("schedule = %+v, %v", sched, err)
	}
	// The older one goes first once due, then the newer.
	due := t0.Add(30 * time.Second)
	first, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t3", due)
	if err != nil || len(first) != 1 || first[0].ID != "older" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if _, err := s.AcknowledgeReportBatch(ctx, "tp", "t3", due); err != nil {
		t.Fatal(err)
	}
	second, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t4", due)
	if err != nil || len(second) != 1 || second[0].ID != "newer" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestReportStoreClaimDoesNotHoldReportsNeedingTheOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, state domain.ReportState, at time.Time) {
		t.Helper()
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: state, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	mk("older", domain.ReportCheckpoint, t0)
	if _, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeferReportBatch(ctx, "tp", "t1", t0.Add(30*time.Second), domain.ReportDeliveryBusyReason); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(5 * time.Second)
	mk("ask", domain.ReportNeedsInput, later)
	got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t2", later)
	if err != nil || len(got) != 1 || got[0].ID != "ask" {
		t.Fatalf("needs_input must be delivered at once, got %+v, %v", got, err)
	}
}

// Reports filed a few seconds apart inside the batch window reach the
// orchestrator as one claim, in authored order, when the first becomes due.
func TestReportStoreClaimBatchesReportsFiledInsideTheWindow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	for i, id := range []string{"a", "b", "c"} {
		at := t0.Add(time.Duration(i*4) * time.Second)
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: domain.ReportCheckpoint, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at.Add(domain.ReportBatchWindow), RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	sched, err := s.ListPendingReportSchedule(ctx, 10)
	if err != nil || len(sched) != 1 || !sched[0].AvailableAt.Equal(t0.Add(domain.ReportBatchWindow)) {
		t.Fatalf("schedule = %+v, %v", sched, err)
	}
	got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0.Add(domain.ReportBatchWindow))
	if err != nil || len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("claim = %+v, %v", got, err)
	}
}

// A head backed off beyond the hold (exhausted or usage-limit) must not be
// claimed early, nor starve a due newer report.
func TestReportStoreClaimSkipsHeadBackedOffBeyondTheHold(t *testing.T) {
	for _, state := range []domain.ReportState{domain.ReportNeedsInput, domain.ReportCheckpoint} {
		t.Run(string(state), func(t *testing.T) {
			s := newTestStore(t)
			ctx := context.Background()
			seedProject(t, s, "tp")
			sess, err := s.CreateSession(ctx, sampleRecord("tp"))
			if err != nil {
				t.Fatal(err)
			}
			t0 := time.Now().UTC().Truncate(time.Second)
			mk := func(id string, st domain.ReportState, at time.Time) {
				t.Helper()
				if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: st, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1}); err != nil {
					t.Fatal(err)
				}
			}
			mk("poisoned", domain.ReportCheckpoint, t0)
			if _, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeferReportBatch(ctx, "tp", "t1", t0.Add(time.Hour), "boom"); err != nil {
				t.Fatal(err)
			}
			later := t0.Add(5 * time.Second)
			mk("newer", state, later)
			got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t2", later)
			if err != nil || len(got) != 1 || got[0].ID != "newer" {
				t.Fatalf("claim = %+v, %v", got, err)
			}
		})
	}
}

// A batch holding one due row is claimed whole, never answered with an empty
// claim (which RunDue would re-list without sleeping).
func TestReportStoreClaimTakesRetryBatchWithADueRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, at, avail time.Time) {
		t.Helper()
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: domain.ReportCheckpoint, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: avail, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	mk("d", t0, t0.Add(15*time.Second))
	mk("p", t0.Add(time.Second), t0.Add(time.Second))
	if got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0.Add(time.Second)); err != nil || len(got) != 2 {
		t.Fatalf("first claim %v %v", got, err)
	}
	// D stays due at +15s while P is deferred only to +5s: mixed rows.
	if _, err := s.DeferReportBatch(ctx, "tp", "t1", t0.Add(5*time.Second), "x"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t2", t0.Add(6*time.Second))
	if err != nil || len(got) != 2 {
		t.Fatalf("batch with a due row must be claimed whole, got %+v, %v", got, err)
	}
}

// Failure backoff (e.g. an unconfirmed batch awaiting manual recovery) must not
// hold newer non-urgent reports even inside the hold window.
func TestReportStoreClaimDoesNotHoldBehindFailureBackoff(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now().UTC().Truncate(time.Second)
	mk := func(id string, at time.Time) {
		t.Helper()
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", State: domain.ReportCheckpoint, Note: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	mk("failing", t0)
	if _, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t1", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeferReportBatch(ctx, "tp", "t1", t0.Add(time.Minute), "worker report delivery unconfirmed"); err != nil {
		t.Fatal(err)
	}
	later := t0.Add(5 * time.Second)
	mk("newer", later)
	got, err := s.ClaimPendingReportBatch(ctx, "tp", "", "t2", later)
	if err != nil || len(got) != 1 || got[0].ID != "newer" {
		t.Fatalf("claim = %+v, %v", got, err)
	}
}
