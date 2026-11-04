package agentaccounts

import (
	"context"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// Codex accounts the Codex account service manages live in the same table but
// are not listed here: Remove would sign out a login that service still serves.
func TestManagedCodexAccountsAreNotListedHere(t *testing.T) {
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	store.accounts = []domain.AgentAccount{
		{ID: "managed-1", Provider: domain.AgentAccountProviderCodex, Home: "/x/managed-1", Source: string(domain.CodexAccountSourceManaged)},
		{ID: "saved-1", Provider: domain.AgentAccountProviderCodex, Home: "/x/saved-1"},
	}
	svc := New(store, []ports.AgentAccountProviderCLI{signOutCodex{&fakeClaude{}}}, t.TempDir(), fakeTerminals{}, nil)
	accounts, err := svc.accounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, a := range accounts {
		ids = append(ids, a.ID)
	}
	if len(ids) != 2 || ids[0] != "device-codex" || ids[1] != "saved-1" {
		t.Fatalf("listed accounts = %v, want the device and saved-1 only", ids)
	}
}
