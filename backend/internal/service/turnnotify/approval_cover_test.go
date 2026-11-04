package turnnotify

import (
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// B426: a worker that filed its needs_input report earlier in the same turn that
// then waits on the review still only acknowledges the approval.
func TestReviewApprovalCoversTurnThatReportedBeforeApproval(t *testing.T) {
	h := newHarness(domain.KindWorker)
	h.signal("user-prompt-submit", "")
	h.o.ReportFiled(domain.ReportRecord{SessionID: "w1", State: domain.ReportCheckpoint})
	h.o.ApprovalReported(approvalRec())
	h.signal("stop", "approved, nothing further to change")
	h.fire()
	if len(h.reports.got) != 0 {
		t.Fatalf("reports = %d, want 0", len(h.reports.got))
	}
}
