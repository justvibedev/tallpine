package report

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The owner-relay strip has to recover the owner's words from exactly what
// piggybacking stores, so a change to the envelope cannot silently disable it.
func TestStripWorkerReportsRecoversPromptFromAppendedBatch(t *testing.T) {
	now := time.Date(2026, 9, 14, 13, 0, 0, 0, time.UTC)
	batch := PreparedBatch{Reports: []domain.ReportRecord{reportAt("info", "worker", now, now.Add(time.Hour), "context")}}
	combined := batch.AppendToUserMessage("hello")
	if combined == "hello" {
		t.Fatal("batch attached no reports")
	}
	if owner := domain.StripWorkerReports(combined); owner != "hello" {
		t.Fatalf("owner text after strip = %q, want the prompt alone", owner)
	}
}
