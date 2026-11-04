package codex

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// The Codex accounts API view stays byte-for-byte what clients already read:
// the snapshot derived from the saved account must not drift.
func TestCodexAccountsViewMatchesGolden(t *testing.T) {
	root := filepath.Join(t.TempDir(), "accounts")
	pending := filepath.Join(filepath.Dir(root), "pending-accounts")
	catalog := newCodexAccountCatalog(root, nil)
	fixed := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	catalog.now = func() time.Time { return fixed }
	ids := []string{"7a1c1f0e-5f3a-4a55-8d2b-1f6a0c9b3e11", "bb1e9a5d-37ad-43f8-83bd-13de8168f8af"}
	catalog.newID = func() string { id := ids[0]; ids = ids[1:]; return id }
	email := "person@example.com"
	signedIn := commitTestAccount(t, catalog, pending, "b60a377d-da68-4a61-86f2-f31f04c571f2", ports.CodexAccountObservation{
		Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodChatGPT, Email: &email,
	})
	out := commitTestAccount(t, catalog, pending, "1c5de3ab-82d0-4a68-a06b-8495cdeab909", ports.CodexAccountObservation{
		Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodAPIKey,
	})
	if _, err := catalog.markSignedOut(out.Snapshot.ID); err != nil {
		t.Fatal(err)
	}
	if err := catalog.refresh(); err != nil {
		t.Fatal(err)
	}
	view := Accounts{Accounts: catalog.snapshots(), ActiveAccountID: signedIn.Snapshot.ID}
	got, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "codex_accounts_view.golden.json")
	if *updateGolden {
		if err := os.WriteFile(path, append(got, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(append(got, '\n')) != string(want) {
		t.Fatalf("accounts view drifted from %s:\n%s", path, got)
	}
}
