package report

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestCreateBatchWindowDelaysOnlyNonUrgentReports(t *testing.T) {
	now := time.Date(2026, 9, 7, 1, 2, 3, 0, time.UTC)
	window := 15 * time.Second
	art := []domain.ReportOutput{{Kind: domain.ReportOutputArtifact, Reference: "result"}}
	tests := []struct {
		name      string
		input     CreateInput
		available time.Time
	}{
		{"done waits the window", CreateInput{State: domain.ReportDone, Note: "finished"}, now.Add(window)},
		{"checkpoint with output waits the window", CreateInput{State: domain.ReportCheckpoint, Note: "doc", Outputs: art}, now.Add(window)},
		{"needs input is not delayed", CreateInput{State: domain.ReportNeedsInput, Note: "decision"}, now},
		{"stuck is not delayed", CreateInput{State: domain.ReportStuck, Note: "blocked"}, now},
		{"daemon notice is not delayed", CreateInput{State: domain.ReportDone, Note: "blocked on a permission prompt", Synthesized: true}, now},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &fakeStore{ok: true, session: domain.SessionRecord{ID: "tp-7", ProjectID: "tp", Kind: domain.KindWorker}}
			s := New(Deps{Store: st, Now: func() time.Time { return now }, NewID: func() string { return "rpt_1" }, BatchWindow: window})
			tc.input.SessionID = "tp-7"
			r, err := s.Create(context.Background(), tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if !r.AvailableAt.Equal(tc.available) {
				t.Fatalf("available=%v want %v", r.AvailableAt, tc.available)
			}
		})
	}
}
