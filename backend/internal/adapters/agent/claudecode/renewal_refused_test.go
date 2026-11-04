package claudecode

import (
	"context"
	"net/http"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/ports"
)

func resetLoginRefusals(t *testing.T) {
	t.Helper()
	reset := func() {
		claudeLoginRefusals.mu.Lock()
		claudeLoginRefusals.at = nil
		claudeLoginRefusals.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func probeStatus(t *testing.T, resolved claudeProviderContext) ports.AgentAuthStatus {
	t.Helper()
	status, ok := (&Plugin{}).probeResolvedAuthStatus(context.Background(), resolved, true)
	if !ok {
		t.Fatal("probe gave no verdict")
	}
	return status
}

// B520: once the provider refused an expired login (Claude Code failed to
// renew it), readiness must say signed out, as launch does, and not
// "configured". The refusal belongs to that credential only: another
// account's expired login may still renew, so it stays configured.
func TestExpiredLoginRefusedByProviderReadsUnauthorized(t *testing.T) {
	InvalidateAuthCache()
	t.Cleanup(InvalidateAuthCache)
	resetLoginRefusals(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	pinClaudeNow(t, now)
	server := withStubValidator(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	})
	accountA := storedLoginContext(now.Add(-time.Hour), true, server.URL)
	accountB := storedLoginContext(now.Add(-time.Hour), true, server.URL)
	accountB.credential.Secret = "sk-ant-oat01-other-account"
	accountB.credential.Source = "config-dir-b"

	noteLoginRefused(accountA)
	if got := probeStatus(t, accountA); got != ports.AgentAuthStatusUnauthorized {
		t.Fatalf("refused account status = %q, want unauthorized", got)
	}
	if got := probeStatus(t, accountB); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("other account status = %q, want configured: its login may still renew", got)
	}

	// A refusal of a login that had not yet expired says nothing about renewal.
	resetLoginRefusals(t)
	fresh := storedLoginContext(now.Add(time.Minute), true, server.URL)
	noteLoginRefused(fresh)
	pinClaudeNow(t, now.Add(2*time.Minute))
	if got := probeStatus(t, fresh); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("status = %q, want configured for a login refused before it expired", got)
	}
}
