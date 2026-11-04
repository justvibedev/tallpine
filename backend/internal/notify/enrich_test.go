package notify

import (
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
)

func TestEnrichReadyToMergePrioritizesPRContext(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:               domain.NotificationReadyToMerge,
		SessionID:          "sess-1",
		ProjectID:          "proj-1",
		PRURL:              "https://github.com/acme/app/pull/67",
		SessionDisplayName: "Checkout flow",
		PRNumber:           67,
		PRTitle:            "Fix checkout totals",
		CreatedAt:          time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "Fix checkout totals · PR #67"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
	if want := "PR from session Checkout flow is ready to merge. CI passed with no blocking review feedback."; rec.Body != want {
		t.Fatalf("body = %q, want %q", rec.Body, want)
	}
}

func TestEnrichReadyToMergeFallsBackWithoutPRTitle(t *testing.T) {
	t.Parallel()

	rec, err := enrich(Intent{
		Type:      domain.NotificationReadyToMerge,
		SessionID: "sess-1",
		ProjectID: "proj-1",
		PRURL:     "https://github.com/acme/app/pull/67",
		PRNumber:  67,
		CreatedAt: time.Date(2026, 8, 5, 4, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("enrich ready notification: %v", err)
	}

	if want := "PR #67 is ready to merge"; rec.Title != want {
		t.Fatalf("title = %q, want %q", rec.Title, want)
	}
}

func TestEnrichOrchestratorApprovalKeepsItsTextWithoutAPR(t *testing.T) {
	rec, err := enrich(Intent{
		Type: domain.NotificationOrchestratorApproval, SessionID: "w-1", CreatedAt: time.Now(),
		Title: "orchestrator:o-1 allowed once a prompt of w-1", Body: "Command: go build /tmp/x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "orchestrator:o-1 allowed once a prompt of w-1" || rec.Body != "Command: go build /tmp/x" || rec.PRURL != "" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestEnrichPlanWaitingKeepsItsTextWithoutAPR(t *testing.T) {
	rec, err := enrich(Intent{
		Type: domain.NotificationPlanWaiting, SessionID: "p-1", CreatedAt: time.Now(),
		Title: "Plan for B1 needs your approval", Body: "Open the plan card to approve it.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "Plan for B1 needs your approval" || rec.Body != "Open the plan card to approve it." || rec.PRURL != "" {
		t.Fatalf("record = %+v", rec)
	}
}

func TestEnrichReviewBodyAppendsHeadMovedNote(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		typ        domain.NotificationType
		note, want string
	}{
		{domain.NotificationReviewCompleted, "Reviewed a, PR is now at b; re-review needed.", "Orchestrator finished reviewing this pull request and approved it. Reviewed a, PR is now at b; re-review needed."},
		{domain.NotificationReviewChangesRequested, "Reviewed a, PR is now at b; re-review needed.", "Orchestrator finished reviewing this pull request and found changes to address. Reviewed a, PR is now at b; re-review needed."},
		{domain.NotificationReviewCompleted, "", "Orchestrator finished reviewing this pull request and approved it."},
		{domain.NotificationReviewChangesRequested, "", "Orchestrator finished reviewing this pull request and found changes to address."},
	} {
		rec, err := enrich(Intent{Type: tc.typ, SessionID: "s", ProjectID: "p", PRURL: "https://github.com/o/r/pull/1", Body: tc.note, CreatedAt: time.Unix(1, 0).UTC()})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Body != tc.want {
			t.Fatalf("%s body = %q, want %q", tc.typ, rec.Body, tc.want)
		}
	}
}
