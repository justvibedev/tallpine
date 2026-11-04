package store_test

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/storage/sqlite/sqlitetest"
)

// The exit reason decides whether a restart may resume an agent, so it must
// round-trip, be replaceable, and read as unknown when absent or cleared.
func TestSessionAgentExitReasonRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := sqlitetest.MustOpen(t)
	seedProject(t, s, "exit")
	rec, err := s.CreateSession(ctx, sampleRecord("exit"))
	if err != nil {
		t.Fatal(err)
	}
	read := func() domain.AgentExitReason {
		t.Helper()
		reason, err := s.SessionAgentExitReason(ctx, rec.ID)
		if err != nil {
			t.Fatal(err)
		}
		return reason
	}
	if got := read(); got != domain.AgentExitUnknown {
		t.Fatalf("fresh session reason = %q, want unknown", got)
	}
	for _, want := range []domain.AgentExitReason{domain.AgentExitUser, domain.AgentExitHostLost, domain.AgentExitUnknown} {
		if err := s.SetSessionAgentExitReason(ctx, rec.ID, want); err != nil {
			t.Fatal(err)
		}
		if got := read(); got != want {
			t.Fatalf("reason = %q, want %q", got, want)
		}
	}
}
