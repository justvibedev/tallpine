package agentaccounts

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

func orgFixture(t *testing.T) (*Service, *fakeClaude, *memStore, string) {
	t.Helper()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}, orgs: map[string]string{"": "org-personal"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	return New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil), claude, store, root
}

func signInNewFolder(t *testing.T, svc *Service, claude *fakeClaude, root, email, org string) (LoginStatus, *AccountView) {
	t.Helper()
	ctx := context.Background()
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "claude", start.OperationID)
	claude.signedInAs[home], claude.orgs[home] = email, org
	status, view, err := svc.VerifyLogin(ctx, start.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	return status, view
}

// The same email in another organization is a different login: it is saved.
// The same email and organization is the device login again: refused.
func TestSameEmailInAnotherOrganizationIsAnotherAccount(t *testing.T) {
	svc, claude, store, root := orgFixture(t)
	if status, _ := signInNewFolder(t, svc, claude, root, "a@example.com", "org-personal"); status != LoginDuplicate {
		t.Fatalf("same email and organization = %q, want duplicate", status)
	}
	status, view := signInNewFolder(t, svc, claude, root, "a@example.com", "org-team")
	if status != LoginComplete || view == nil || view.Account.OrgID != "org-team" || view.DuplicateOf != "" {
		t.Fatalf("same email, other organization = %q view=%+v", status, view)
	}
	if len(store.accounts) != 1 || store.accounts[0].OrgID != "org-team" || store.accounts[0].OrgName == "" {
		t.Fatalf("saved = %+v, want the team organization stored", store.accounts)
	}
}

// An entry saved twice before duplicates were refused is flagged, not hidden.
func TestListFlagsAnEntryThatRepeatsALogin(t *testing.T) {
	svc, claude, store, root := orgFixture(t)
	home := filepath.Join(root, "claude", "old")
	claude.signedInAs[home], claude.orgs[home] = "a@example.com", "org-personal"
	store.accounts = []domain.AgentAccount{{ID: "old", Provider: domain.AgentAccountProviderClaude, Home: home, Email: "a@example.com"}}
	views, err := svc.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	var device, saved AccountView
	for _, v := range views {
		if v.Account.ID == "old" {
			saved = v
		} else if v.Account.Device() {
			device = v
		}
	}
	if device.DuplicateOf != "" || saved.DuplicateOf != device.Account.ID {
		t.Fatalf("duplicateOf device=%q saved=%q, want saved to repeat %q", device.DuplicateOf, saved.DuplicateOf, device.Account.ID)
	}
	if store.accounts[0].OrgID != "org-personal" {
		t.Fatalf("organization not learned on read: %+v", store.accounts[0])
	}
}

func TestSetLabelNamesASavedAccountOnly(t *testing.T) {
	svc, claude, store, root := orgFixture(t)
	_, view := signInNewFolder(t, svc, claude, root, "a@example.com", "org-team")
	id := view.Account.ID
	if err := svc.SetLabel(context.Background(), id, "  Work  "); err != nil || store.accounts[0].Label != "Work" {
		t.Fatalf("label = %q err=%v", store.accounts[0].Label, err)
	}
	if err := svc.SetLabel(context.Background(), domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), "x"); !errors.Is(err, ports.ErrAgentAccountDeviceLabel) {
		t.Fatalf("device label err = %v", err)
	}
	if err := svc.SetLabel(context.Background(), "nope", "x"); !errors.Is(err, ports.ErrAgentAccountUnknown) {
		t.Fatalf("unknown label err = %v", err)
	}
}

// A folder signed in again to another organization under the same email is
// refused, and the message names the organizations rather than repeating the email.
func TestSameEmailOtherOrganizationMismatchNamesTheOrganizations(t *testing.T) {
	svc, claude, store, root := orgFixture(t)
	ctx := context.Background()
	_, view := signInNewFolder(t, svc, claude, root, "a@example.com", "org-team")
	home := view.Account.Home
	claude.orgs[home] = "org-other"
	views, err := svc.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var problem string
	for _, v := range views {
		if v.Account.ID == view.Account.ID {
			problem = v.IdentityErr
		}
	}
	if problem != "the folder is now signed in to Org org-other, not Org org-team" {
		t.Fatalf("problem = %q", problem)
	}
	_ = store
	again, err := svc.StartSignInAgain(ctx, view.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if status, _, err := svc.VerifyLogin(ctx, again.OperationID); err != nil || status != LoginMismatch {
		t.Fatalf("sign in again to another organization = %q err=%v", status, err)
	}
}
