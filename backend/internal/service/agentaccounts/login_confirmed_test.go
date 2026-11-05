package agentaccounts

import (
	"context"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func TestAReadingThatReachesTheProviderConfirmsTheLogin(t *testing.T) {
	ctx := context.Background()
	claude := &fakeClaude{signedInAs: map[string]string{}, failing: map[string]bool{}}
	svc := New(&memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}},
		[]ports.AgentAccountProviderCLI{claude}, t.TempDir(), fakeTerminals{}, nil)
	at := time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return at }
	var confirmed []string
	svc.SetLoginConfirmed(func(account domain.AgentAccount, checkedAt time.Time) {
		if !checkedAt.Equal(at) {
			t.Errorf("confirmed at %v, want the reading's start %v", checkedAt, at)
		}
		confirmed = append(confirmed, account.ID)
	})
	read := func() {
		if _, err := svc.List(ctx, true); err != nil {
			t.Fatal(err)
		}
	}
	read() // signed out
	claude.failing[""] = true
	read() // the check fails
	if len(confirmed) != 0 {
		t.Fatalf("confirmed without a working login: %v", confirmed)
	}
	// Signed in with the CLI's own login, outside AO.
	claude.failing[""] = false
	claude.signedInAs[""] = "a@example.com"
	read()
	if len(confirmed) != 1 || confirmed[0] != domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude) {
		t.Fatalf("confirmed = %v", confirmed)
	}
}
