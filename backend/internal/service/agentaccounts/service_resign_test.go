package agentaccounts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// resignFixture is a saved Claude account b (own folder holding a credentials
// file) next to the computer's own folder, which holds one too.
type resignFixture struct {
	svc          *Service
	store        *memStore
	claude       *fakeClaude
	accountID    string
	home         string
	deviceCreds  string
	accountCreds string
}

func newResignFixture(t *testing.T) resignFixture {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	device := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude}, root, fakeTerminals{}, nil)
	id := signInSecondAccount(t, svc, claude.signedInAs, root, domain.AgentAccountProviderClaude)
	f := resignFixture{svc: svc, store: store, claude: claude, accountID: id, home: filepath.Join(root, "claude", id)}
	f.accountCreds = filepath.Join(f.home, ".credentials.json")
	f.deviceCreds = filepath.Join(device, ".credentials.json")
	for _, p := range []string{f.accountCreds, f.deviceCreds} {
		if err := os.WriteFile(p, []byte(`{"claudeAiOauth":{}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.List(ctx, true); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f resignFixture) assertClearedLocally(t *testing.T) {
	t.Helper()
	if f.claude.logouts.Load() != 0 {
		t.Fatalf("CLI sign-outs = %d, want 0: it would revoke another login's tokens", f.claude.logouts.Load())
	}
	if _, err := os.Stat(f.accountCreds); !os.IsNotExist(err) {
		t.Fatalf("the account folder's credentials file stayed: %v", err)
	}
	if _, err := os.Stat(f.deviceCreds); err != nil {
		t.Fatalf("the computer's own credentials file was touched: %v", err)
	}
	views, err := f.svc.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Account.ID == f.accountID && v.SignedIn {
			t.Fatal("the account still reads as signed in")
		}
	}
}

// Signing in again as a different login leaves nothing to sign out through the
// CLI: the folder's credentials are cleared locally and the account is signed out.
func TestResignInAsAnotherLoginClearsTheFolderWithoutTheCLI(t *testing.T) {
	f := newResignFixture(t)
	again, err := f.svc.StartSignInAgain(context.Background(), f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	f.claude.signedInAs[f.home] = "c@example.com"
	if status, _, err := f.svc.VerifyLogin(context.Background(), again.OperationID); err != nil || status != LoginMismatch {
		t.Fatalf("status = %q, %v", status, err)
	}
	f.assertClearedLocally(t)
}

// Signing in again as the computer's own login is a duplicate of it; the CLI
// sign-out there would revoke that user's tokens, so it is never run.
func TestResignInAsTheComputersLoginClearsTheFolderWithoutTheCLI(t *testing.T) {
	f := newResignFixture(t)
	again, err := f.svc.StartSignInAgain(context.Background(), f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	f.claude.signedInAs[f.home] = "a@example.com" // the device's identity
	if status, _, err := f.svc.VerifyLogin(context.Background(), again.OperationID); err != nil || status != LoginDuplicate {
		t.Fatalf("status = %q, %v", status, err)
	}
	f.assertClearedLocally(t)
	// A later good sign-in clears the mark.
	again, _ = f.svc.StartSignInAgain(context.Background(), f.accountID)
	f.claude.signedInAs[f.home] = "b@example.com"
	if status, _, err := f.svc.VerifyLogin(context.Background(), again.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("status = %q, %v", status, err)
	}
	views, _ := f.svc.List(context.Background(), true)
	for _, v := range views {
		if v.Account.ID == f.accountID && !v.SignedIn {
			t.Fatal("the account stayed signed out after a good sign-in")
		}
	}
}

// An account saved without an email has no identity to compare, so only the
// local mark keeps it signed out after the folder is cleared.
func TestLocallyClearedAccountStaysSignedOutWithoutAnEmail(t *testing.T) {
	f := newResignFixture(t)
	f.store.accounts[0].Email = ""
	again, err := f.svc.StartSignInAgain(context.Background(), f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	f.claude.signedInAs[f.home] = "a@example.com"
	if status, _, err := f.svc.VerifyLogin(context.Background(), again.OperationID); err != nil || status != LoginDuplicate {
		t.Fatalf("status = %q, %v", status, err)
	}
	f.assertClearedLocally(t)
}

func refuse(t *testing.T, f resignFixture) {
	t.Helper()
	again, err := f.svc.StartSignInAgain(context.Background(), f.accountID)
	if err != nil {
		t.Fatal(err)
	}
	f.claude.signedInAs[f.home] = "c@example.com"
	if status, _, err := f.svc.VerifyLogin(context.Background(), again.OperationID); err != nil || status != LoginMismatch {
		t.Fatalf("status = %q, %v", status, err)
	}
}

// A hand-made link in place of the account folder is not followed: nothing in
// what it points at is removed.
func TestLocalSignOutRefusesASymlinkedAccountFolder(t *testing.T) {
	f := newResignFixture(t)
	elsewhere := filepath.Join(filepath.Dir(f.home), "other-folder")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(elsewhere, ".credentials.json")
	if err := os.WriteFile(theirs, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.home); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, f.home); err != nil {
		t.Fatal(err)
	}
	refuse(t, f)
	if _, err := os.Stat(theirs); err != nil {
		t.Fatalf("a file behind the symlinked folder was removed: %v", err)
	}
}

// A credentials entry that is a link loses only the link.
func TestLocalSignOutRemovesACredentialsLinkNotItsTarget(t *testing.T) {
	f := newResignFixture(t)
	target := filepath.Join(t.TempDir(), "precious")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.accountCreds); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.accountCreds); err != nil {
		t.Fatal(err)
	}
	refuse(t, f)
	if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
		t.Fatalf("the link's target was touched: %q %v", b, err)
	}
	if _, err := os.Lstat(f.accountCreds); !os.IsNotExist(err) {
		t.Fatalf("the credentials link stayed: %v", err)
	}
}

// Anything but a file or link in that place is skipped.
func TestLocalSignOutSkipsACredentialsDirectory(t *testing.T) {
	f := newResignFixture(t)
	if err := os.Remove(f.accountCreds); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.accountCreds, 0o700); err != nil { // empty: a plain remove would succeed
		t.Fatal(err)
	}
	refuse(t, f)
	if info, err := os.Lstat(f.accountCreds); err != nil || !info.IsDir() {
		t.Fatalf("a directory in the credentials place was removed: %v", err)
	}
}
