package review

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B426: the worker's `review wait` returns once the run is complete, so the
// approval report must exist by the time Submit returns, not after a sweep.
func TestSubmitApprovedFilesTheApprovalReportBeforeReturning(t *testing.T) {
	ctx := context.Background()
	f := newQuietFixture(t)
	seedEndedRun(t, f.st, "only", "w-1", "abc123", domain.ReviewRunRunning, "", *f.now)
	svc := New(nil, f.st)
	svc.SetFailureNotice(f.st, f.reports, &fakeWorkerNudger{})
	if _, err := svc.Submit(ctx, "w-1", "only", domain.VerdictApproved, "", ""); err != nil {
		t.Fatal(err)
	}
	_, found, err := f.st.GetReport(ctx, domain.ReviewApprovedReportIDPrefix+"only")
	if err != nil || !found {
		t.Fatalf("approval report not filed by Submit: found=%v err=%v", found, err)
	}
}
