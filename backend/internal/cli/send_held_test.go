package cli

import (
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/httpd/apitypes"
)

func TestDescribeSendOutcomeReportsUsageLimitHold(t *testing.T) {
	held := describeSendOutcome("w-1", apitypes.SendSessionMessageResponse{Outcome: "queued", HeldUntil: "2026-08-02T17:10:00Z"})
	if !strings.Contains(held, "held until ") || !strings.Contains(held, "by usage-limit pause") {
		t.Fatalf("held outcome = %q", held)
	}
	plain := describeSendOutcome("w-1", apitypes.SendSessionMessageResponse{Outcome: "queued"})
	if strings.Contains(plain, "held until") {
		t.Fatalf("plain queued outcome mentions a hold: %q", plain)
	}
}
