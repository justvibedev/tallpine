package report

import (
	"os"
	"regexp"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

// The desktop and mobile report-card parsers read this same file
// (packages/shared/chat/automation/worker-reports.test.ts),
// so a change to Render's format fails here until the fixture, and with it the clients, are updated.
// Run with UPDATE_GOLDEN=1 to rewrite it.
const renderGoldenPath = "../../../../packages/shared/testdata/report-render.golden"

var frameNonce = regexp.MustCompile(`n=[0-9a-f]{8}`)

func TestRenderMatchesClientGoldenFixture(t *testing.T) {
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	got := frameNonce.ReplaceAllString((PreparedBatch{Reports: []domain.ReportRecord{
		{ID: "r1", SessionID: "app-1", ProjectID: "proj", SessionDisplayName: "Fix login", State: domain.ReportDone, Note: "Opened the PR.\nTests pass.", CreatedAt: at,
			Outputs: []domain.ReportOutput{{Kind: domain.ReportOutputPRCreated, Reference: "https://github.com/o/r/pull/1"}, {Kind: domain.ReportOutputArtifact, Reference: "notes.md"}}},
		{ID: "r2", SessionID: "app-2", ProjectID: "proj", SessionDisplayName: "Docs", State: domain.ReportNeedsInput, Note: "Which branch?", CreatedAt: at.Add(time.Minute)},
		{ID: "r3", SessionID: "app-2", ProjectID: "proj", SessionDisplayName: "Docs", Synthesized: true, State: domain.ReportStuck, Note: "[auto] Turn ended.\nLast message: " + domain.FrameUntrusted("app-2", "done"), CreatedAt: at.Add(2 * time.Minute)},
	}}).Render(), "n=00000000")
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(renderGoldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(renderGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("Render drifted from %s (UPDATE_GOLDEN=1 rewrites it; then the client parsers must still parse it):\n%s", renderGoldenPath, got)
	}
}
