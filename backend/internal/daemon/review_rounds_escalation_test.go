package daemon

import (
	"context"
	"log/slog"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	reportsvc "tallpine.eu/tallpine/backend/internal/service/report"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

func TestEscalateReviewRoundsFilesOneNeedsInputReport(t *testing.T) {
	ctx := context.Background()
	st := sqlitetest.MustOpen(t)
	if err := st.UpsertProject(ctx, domain.ProjectRecord{ID: "p1", Path: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.CreateSession(ctx, domain.SessionRecord{ID: "p1-1", ProjectID: "p1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatal(err)
	}
	b := &boot{reportSvc: reportsvc.New(reportsvc.Deps{Store: st}), log: slog.Default()}
	b.escalateReviewRounds(ctx, sess, "https://github.com/o/r/pull/9", "sha-a", 3)
	b.escalateReviewRounds(ctx, sess, "https://github.com/o/r/pull/9", "sha-a", 3)
	reports, err := st.ListReportsByProject(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 1 || reports[0].State != domain.ReportNeedsInput || !reports[0].Synthesized {
		t.Fatalf("reports = %+v, want one synthesized needs_input report", reports)
	}
	// A later stuck head is a new decision for the orchestrator.
	b.escalateReviewRounds(ctx, sess, "https://github.com/o/r/pull/9", "sha-b", 3)
	if reports, _ = st.ListReportsByProject(ctx, "p1"); len(reports) != 2 {
		t.Fatalf("reports after a second stuck head = %d, want 2", len(reports))
	}
}
