package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite"
)

func TestReportStorePersistsOutputsAndDeadlinesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	want := domain.ReportRecord{
		ID: "rpt_1", SessionID: sess.ID, ProjectID: sess.ProjectID,
		Outputs: []domain.ReportOutput{
			{Kind: domain.ReportOutputArtifact, Reference: "opaque-ref", Label: "Result"},
			{Kind: domain.ReportOutputArtifact, Reference: "second"},
			{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"},
		},
		CreatedAt: now, DeliveryState: domain.ReportPending,
		AvailableAt: now.Add(time.Hour), RepeatCount: 1,
	}
	if _, err = s.CreateReport(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, ok, err := s.GetReport(ctx, want.ID)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.SessionID != sess.ID || got.ProjectID != sess.ProjectID || len(got.Outputs) != 3 || got.Outputs[0].Label != "Result" || got.Outputs[1].Reference != "second" || got.Outputs[2].Kind != domain.ReportOutputPRCreated || !got.AvailableAt.Equal(want.AvailableAt) {
		t.Fatalf("got=%+v", got)
	}
	if pending, err := s.ListPendingReports(ctx, now, 10); err != nil || len(pending) != 0 {
		t.Fatalf("early pending=%+v err=%v", pending, err)
	}
	urls, err := s.ListReportedPRURLs(ctx, sess.ID)
	if err != nil || len(urls) != 1 || urls[0] != "https://github.com/o/r/pull/1" {
		t.Fatalf("reported PR URLs=%v err=%v", urls, err)
	}
	events, err := s.EventsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var reportEvents int
	for _, event := range events {
		if event.SessionID == string(sess.ID) && event.Type == "session_updated" {
			reportEvents++
		}
	}
	if reportEvents != 1 {
		t.Fatalf("reported PR CDC events=%d, want 1", reportEvents)
	}
	pending, err := s.ListPendingReports(ctx, want.AvailableAt, 10)
	if err != nil || len(pending) != 1 || pending[0].ID != want.ID || len(pending[0].Outputs) != 3 {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
}

func TestReportStoreRejectsInvalidRecord(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateReport(context.Background(), domain.ReportRecord{})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestReportStoreClaimAcknowledgeAndTokenFence(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_lease", SessionID: sess.ID, ProjectID: sess.ProjectID, State: domain.ReportDone, Note: "done", CreatedAt: now.Add(-domain.ReportSettlementWindow), DeliveryState: domain.ReportPending, AvailableAt: now, SettlementDeadline: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "lease-1", now)
	if err != nil || !ok || claimed.DeliveryAttempts != 1 || claimed.DeliveryState != domain.ReportClaimed {
		t.Fatalf("claimed=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err = s.ClaimReport(ctx, rec.ID, "lease-2", now); err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
	if _, ok, err = s.AcknowledgeReport(ctx, rec.ID, "wrong", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("wrong ack ok=%v err=%v", ok, err)
	}
	acked, ok, err := s.AcknowledgeReport(ctx, rec.ID, "lease-1", now.Add(time.Second))
	if err != nil || !ok || acked.DeliveryState != domain.ReportAcknowledged {
		t.Fatalf("acked=%+v ok=%v err=%v", acked, ok, err)
	}
}

func TestReportStoreRequeuesInterruptedClaimOnRestartAndFencesOldToken(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rec := domain.ReportRecord{ID: "rpt_restart", SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "information", CreatedAt: now.Add(-time.Hour), DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}
	if _, err = s.CreateReport(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ClaimReport(ctx, rec.ID, "old-token", now); err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if count, err := s.RequeueClaimedReports(ctx); err != nil || count != 1 {
		t.Fatalf("restart recovery count=%d err=%v", count, err)
	}
	recovered, ok, err := s.GetReport(ctx, rec.ID)
	if err != nil || !ok || recovered.DeliveryState != domain.ReportPending || recovered.ClaimToken != "" || !recovered.ClaimedAt.IsZero() || recovered.DeliveryAttempts != 1 || !strings.Contains(recovered.LastError, "restart") {
		t.Fatalf("recovered=%+v ok=%v err=%v", recovered, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(time.Second)); err != nil || ok {
		t.Fatalf("stale acknowledge ok=%v err=%v", ok, err)
	}
	claimed, ok, err := s.ClaimReport(ctx, rec.ID, "new-token", now.Add(2*time.Second))
	if err != nil || !ok || claimed.DeliveryAttempts != 2 {
		t.Fatalf("reclaim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "old-token", now.Add(3*time.Second)); err != nil || ok {
		t.Fatalf("old token crossed fence ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.AcknowledgeReport(ctx, rec.ID, "new-token", now.Add(4*time.Second)); err != nil || !ok {
		t.Fatalf("new acknowledge ok=%v err=%v", ok, err)
	}
}

func TestReportStoreDurableBatchIdentityExcludesLaterReportsOnRetry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	create := func(id string) {
		t.Helper()
		_, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: "tp", Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	create("first")
	create("second")
	claimed, err := s.ClaimPendingReportBatch(ctx, "tp", "", "token-1", now)
	if err != nil || len(claimed) != 2 || claimed[0].DeliveryBatchID == "" || claimed[0].DeliveryBatchID != claimed[1].DeliveryBatchID {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	batchID := claimed[0].DeliveryBatchID
	if _, err := s.ReleaseReportBatch(ctx, "tp", "token-1", now, "accepted response lost"); err != nil {
		t.Fatal(err)
	}
	create("later")
	retried, err := s.ClaimPendingReportBatch(ctx, "tp", "", "token-2", now)
	if err != nil || len(retried) != 2 || retried[0].DeliveryBatchID != batchID {
		t.Fatalf("retried = %+v, err = %v", retried, err)
	}
	if _, err := s.AcknowledgeReportBatch(ctx, "tp", "token-2", now); err != nil {
		t.Fatal(err)
	}
	next, err := s.ClaimPendingReportBatch(ctx, "tp", "", "token-3", now)
	if err != nil || len(next) != 1 || next[0].ID != "later" || next[0].DeliveryBatchID == batchID {
		t.Fatalf("next = %+v, err = %v", next, err)
	}
}

func TestReportStoreInterruptLimitSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if acquired, _, err := s.AcquireReportInterrupt(ctx, sess.ID, now, 3*time.Minute); err != nil || !acquired {
		t.Fatalf("first acquired = %v, err = %v", acquired, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if acquired, next, err := s.AcquireReportInterrupt(ctx, sess.ID, now.Add(2*time.Minute), 3*time.Minute); err != nil || acquired || !next.Equal(now.Add(3*time.Minute)) {
		t.Fatalf("restart acquired = %v, err = %v", acquired, err)
	}
	if acquired, _, err := s.AcquireReportInterrupt(ctx, sess.ID, now.Add(3*time.Minute), 3*time.Minute); err != nil || !acquired {
		t.Fatalf("boundary acquired = %v, err = %v", acquired, err)
	}
}

func TestReportStoreScheduleListsEarliestPendingPerProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	create := func(project string, id string, at time.Time) {
		t.Helper()
		sess, err := s.CreateSession(ctx, sampleRecord(project))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: domain.ProjectID(project), Message: id, CreatedAt: at, DeliveryState: domain.ReportPending, AvailableAt: at, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	seedProject(t, s, "orphan")
	seedProject(t, s, "live")
	create("orphan", "orphan-1", now.Add(-3*time.Hour))
	create("orphan", "orphan-2", now.Add(-2*time.Hour))
	create("live", "live-1", now.Add(-time.Hour))
	create("live", "live-2", now)

	got, err := s.ListPendingReportSchedule(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "orphan-1" || got[1].ID != "live-1" {
		t.Fatalf("schedule = %+v, want earliest pending per project", got)
	}
}

func TestReportStoreDropsOnlyPendingProjectReportsWithAReason(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	seedProject(t, s, "gone")
	seedProject(t, s, "other")
	create := func(project, id string) {
		t.Helper()
		sess, err := s.CreateSession(ctx, sampleRecord(project))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: domain.ProjectID(project), Message: id, CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}); err != nil {
			t.Fatal(err)
		}
	}
	create("gone", "gone-pending")
	create("gone", "gone-claimed")
	create("other", "other-pending")
	if _, ok, err := s.ClaimReport(ctx, "gone-claimed", "lease", now); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}

	dropped, err := s.DropPendingProjectReports(ctx, "gone", now, "not delivered: project gone was archived")
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0].ID != "gone-pending" {
		t.Fatalf("dropped = %+v, want only the pending report of project gone", dropped)
	}
	reports, err := s.ListReportsByProject(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		switch r.ID {
		case "gone-pending":
			if r.DeliveryState != domain.ReportAcknowledged || r.UndeliveredReason() != "not delivered: project gone was archived" {
				t.Fatalf("dropped report = %+v, want settled with its reason", r)
			}
		case "gone-claimed":
			if r.DeliveryState != domain.ReportClaimed || r.UndeliveredReason() != "" {
				t.Fatalf("claimed report = %+v, want left mid-delivery", r)
			}
		}
	}
	other, err := s.ListReportsByProject(ctx, "other")
	if err != nil || len(other) != 1 || other[0].DeliveryState != domain.ReportPending {
		t.Fatalf("other project = %+v, %v; want untouched", other, err)
	}
	if _, err := s.DropPendingProjectReports(ctx, "gone", now, " "); err == nil {
		t.Fatal("a drop without a reason must be refused")
	}
}

func TestReportStoreDropsOneClaimedReportWithAReason(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	seedProject(t, s, "p")
	sess, err := s.CreateSession(ctx, sampleRecord("p"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateReport(ctx, domain.ReportRecord{ID: "r", SessionID: sess.ID, ProjectID: "p", Message: "m", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.DropClaimedReport(ctx, "r", "lease", now, "why"); err != nil || ok {
		t.Fatalf("drop of an unclaimed report: ok=%v err=%v, want refused", ok, err)
	}
	if _, ok, err := s.ClaimReport(ctx, "r", "lease", now); err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if ok, err := s.DropClaimedReport(ctx, "r", "other-lease", now, "why"); err != nil || ok {
		t.Fatalf("drop with a foreign lease: ok=%v err=%v, want refused", ok, err)
	}
	if ok, err := s.DropClaimedReport(ctx, "r", "lease", now, "stale: PR merged"); err != nil || !ok {
		t.Fatalf("drop: ok=%v err=%v", ok, err)
	}
	got, _, _ := s.GetReport(ctx, "r")
	if got.DeliveryState != domain.ReportAcknowledged || got.UndeliveredReason() != "stale: PR merged" {
		t.Fatalf("report = %+v, want settled with its reason", got)
	}
}

func TestReportRestartReleasesOnlyInitialDelays(t *testing.T) {
	for _, kind := range []string{"checkpoint", "info", "outputs", "done", "retry", "batch retry", "claimed", "acknowledged"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			s, err := sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			seedProject(t, s, "p")
			worker, err := s.CreateSession(ctx, sampleRecord("p"))
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
			r := domain.ReportRecord{ID: "legacy", SessionID: worker.ID, ProjectID: worker.ProjectID,
				State: domain.ReportCheckpoint, Note: "progress", CreatedAt: now, AvailableAt: now.Add(time.Hour), DeliveryState: domain.ReportPending, RepeatCount: 1}
			switch kind {
			case "info":
				r.State, r.Note, r.Message = "", "", "update"
			case "outputs":
				r.State, r.Note, r.Outputs = "", "", []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result.html"}}
			case "done":
				r.State, r.AvailableAt, r.SettlementDeadline = domain.ReportDone, now.Add(5*time.Minute), now.Add(5*time.Minute)
			}
			if _, err := s.CreateReport(ctx, r); err != nil {
				t.Fatal(err)
			}
			wantAt, wantState, attempts := now, domain.ReportPending, int64(0)
			switch kind {
			case "retry", "claimed", "acknowledged":
				if _, ok, err := s.ClaimReport(ctx, r.ID, "old", r.AvailableAt); err != nil || !ok {
					t.Fatalf("claim = %v, %v", ok, err)
				}
				wantAt, attempts = r.AvailableAt, 1
				if kind == "retry" {
					wantAt = now.Add(2 * time.Hour)
					if _, ok, err := s.ReleaseReport(ctx, r.ID, "old", wantAt, "retry"); err != nil || !ok {
						t.Fatalf("release = %v, %v", ok, err)
					}
				}
				if kind == "acknowledged" {
					wantState = domain.ReportAcknowledged
					if _, ok, err := s.AcknowledgeReport(ctx, r.ID, "old", r.AvailableAt); err != nil || !ok {
						t.Fatalf("ack = %v, %v", ok, err)
					}
				}
			case "batch retry":
				if _, err := s.ClaimPendingReportBatch(ctx, worker.ProjectID, "", "old", now); err != nil {
					t.Fatal(err)
				}
				wantAt, attempts = now.Add(2*time.Hour), 1
				if _, err := s.DeferReportBatch(ctx, worker.ProjectID, "old", wantAt, "busy"); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := s.GetReport(ctx, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = sqlite.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			// Recovery is repeatable, and its return value still counts only claims.
			for pass := 0; pass < 2; pass++ {
				wantCount := int64(0)
				if kind == "claimed" && pass == 0 {
					wantCount = 1
				}
				if count, err := s.RequeueClaimedReports(ctx); err != nil || count != wantCount {
					t.Fatalf("recover = %d, %v; want %d", count, err, wantCount)
				}
				got, _, err := s.GetReport(ctx, r.ID)
				if err != nil || !got.AvailableAt.Equal(wantAt) || got.DeliveryState != wantState || got.DeliveryAttempts != attempts || got.DeliveryBatchID != before.DeliveryBatchID || !got.SettlementDeadline.Equal(r.SettlementDeadline) || len(got.Outputs) != len(r.Outputs) {
					t.Fatalf("recovered = %+v, %v", got, err)
				}
			}
			if attempts == 0 {
				pending, err := s.ListPendingReports(ctx, now, 10)
				if err != nil || len(pending) != 1 {
					t.Fatalf("immediately pending = %+v, %v", pending, err)
				}
			}
		})
	}
}

// The renderer decides framing from Synthesized, so every read path (get, list,
// claim) must return it; the flag used to be dropped when the row was rebuilt.
func TestReportStorePersistsSynthesizedFlag(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "tp")
	sess, err := s.CreateSession(ctx, sampleRecord("tp"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for id, synth := range map[string]bool{"rpt_auto": true, "rpt_worker": false} {
		rec := domain.ReportRecord{ID: id, SessionID: sess.ID, ProjectID: sess.ProjectID, Message: "n", CreatedAt: now, DeliveryState: domain.ReportPending, AvailableAt: now, RepeatCount: 1, Synthesized: synth}
		if _, err = s.CreateReport(ctx, rec); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.GetReport(ctx, id)
		if err != nil || !ok || got.Synthesized != synth {
			t.Fatalf("%s get: synthesized=%v ok=%v err=%v", id, got.Synthesized, ok, err)
		}
		claimed, ok, err := s.ClaimReport(ctx, id, "t-"+id, now)
		if err != nil || !ok || claimed.Synthesized != synth {
			t.Fatalf("%s claim: synthesized=%v ok=%v err=%v", id, claimed.Synthesized, ok, err)
		}
	}
	list, err := s.ListReportsBySession(ctx, sess.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	for _, r := range list {
		if r.Synthesized != (r.ID == "rpt_auto") {
			t.Fatalf("list %s synthesized=%v", r.ID, r.Synthesized)
		}
	}
}
