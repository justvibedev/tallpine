package codex

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type registryRows struct{ accounts []domain.AgentAccount }

func (r *registryRows) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return append([]domain.AgentAccount(nil), r.accounts...), nil
}
func (r *registryRows) CreateAgentAccount(_ context.Context, a domain.AgentAccount) error {
	r.accounts = append(r.accounts, a)
	return nil
}
func (r *registryRows) DeleteAgentAccount(_ context.Context, id string) error {
	for i, a := range r.accounts {
		if a.ID == id {
			r.accounts = append(r.accounts[:i], r.accounts[i+1:]...)
		}
	}
	return nil
}
func (r *registryRows) UpdateAgentAccountCodexDescriptor(_ context.Context, id, email, method, providerID string, verified time.Time) error {
	for i, a := range r.accounts {
		if a.ID == id {
			r.accounts[i].Email, r.accounts[i].AuthMethod, r.accounts[i].ProviderAccountID = email, method, providerID
			r.accounts[i].VerifiedAt = &verified
			return nil
		}
	}
	return ports.ErrAgentAccountUnknown
}

// Managed accounts persist as AgentAccount rows and survive a restart (a new
// catalog over the same rows and folders lists them again); other saved
// accounts in the same table are ignored.
func TestCatalogPersistsManagedAccountsAsAgentAccountRows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	pending := filepath.Join(filepath.Dir(root), "pending-accounts")
	rows := &registryRows{accounts: []domain.AgentAccount{{ID: "other-saved", Provider: domain.AgentAccountProviderCodex, Home: "/x/other"}}}
	catalog := newCodexAccountCatalog(root, nil)
	catalog.descriptors = accountRowDescriptors{store: rows}
	catalog.newID = func() string { return testAccountID }
	email := "person@example.com"
	record := commitTestAccount(t, catalog, pending, "b60a377d-da68-4a61-86f2-f31f04c571f2", ports.CodexAccountObservation{
		Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodChatGPT, Email: &email,
	})
	if len(rows.accounts) != 2 {
		t.Fatalf("rows = %+v", rows.accounts)
	}
	row := rows.accounts[1]
	if row.ID != testAccountID || row.Provider != domain.AgentAccountProviderCodex || !row.Managed() || row.Home != record.Home && row.Home != accountHome(root, testAccountID) ||
		row.Email != email || row.AuthMethod != string(domain.CodexAuthMethodChatGPT) || row.VerifiedAt == nil {
		t.Fatalf("saved row = %+v", row)
	}

	restarted := newCodexAccountCatalog(root, nil)
	restarted.descriptors = accountRowDescriptors{store: rows}
	if err := restarted.refresh(); err != nil {
		t.Fatal(err)
	}
	got := restarted.snapshots()
	if len(got) != 1 || got[0].ID != testAccountID || got[0].Status != domain.CodexAccountStatusValid {
		t.Fatalf("accounts after restart = %#v", got)
	}
}

type failingRows struct{ registryRows }

func (*failingRows) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return nil, context.DeadlineExceeded
}

// An unreadable account store is reported as that, not as an invalid account.
func TestUnreadableAccountStoreIsNotReportedAsInvalidDescriptor(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	catalog := newCodexAccountCatalog(root, nil)
	catalog.descriptors = accountRowDescriptors{store: &failingRows{}}
	if err := ensurePrivateDirectory(filepath.Join(root, testAccountID)); err != nil {
		t.Fatal(err)
	}
	record := catalog.readManaged(testAccountID)
	if record.Snapshot.Status != domain.CodexAccountStatusBroken || record.Snapshot.Reason != "The saved Codex accounts could not be read." {
		t.Fatalf("record = %+v", record.Snapshot)
	}
}
