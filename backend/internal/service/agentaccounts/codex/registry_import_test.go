package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

type importStore struct {
	ports.AgentAccountStore
	accounts []domain.AgentAccount
}

func (s *importStore) ListAgentAccounts(context.Context) ([]domain.AgentAccount, error) {
	return append([]domain.AgentAccount(nil), s.accounts...), nil
}

func (s *importStore) CreateAgentAccount(_ context.Context, a domain.AgentAccount) error {
	s.accounts = append(s.accounts, a)
	return nil
}

func descriptorBytes(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			data, _ := os.ReadFile(path)
			out[path] = string(data)
		}
		return nil
	})
	return out
}

func TestImportRegistryKeepsIDsIsIdempotentAndLeavesFilesAlone(t *testing.T) {
	root := t.TempDir()
	email := "a@example.com"
	oneID, twoID := "b60a377d-da68-4a61-86f2-f31f04c571f2", "1c5de3ab-82d0-4a68-a06b-8495cdeab909"
	one := writeLegacyAccount(t, root, oneID, string(domain.CodexAuthMethodChatGPT), &email)
	two := writeLegacyAccount(t, root, twoID, string(domain.CodexAuthMethodAPIKey), nil)
	before := descriptorBytes(t, root)
	store := &importStore{}

	first, err := ImportRegistry(context.Background(), root, store, nil)
	if err != nil || first.Imported != 2 {
		t.Fatalf("first import = %+v, %v", first, err)
	}
	second, err := ImportRegistry(context.Background(), root, store, nil)
	if err != nil || second.Imported != 0 || second.Existing != 2 || len(store.accounts) != 2 {
		t.Fatalf("second import = %+v, %v, accounts=%d", second, err, len(store.accounts))
	}
	ids := map[string]domain.AgentAccount{}
	for _, a := range store.accounts {
		ids[a.ID] = a
	}
	got := ids[one.id]
	if got.Provider != domain.AgentAccountProviderCodex || got.Home != one.home || got.Email != email ||
		got.Source != string(domain.CodexAccountSourceManaged) || got.AuthMethod != string(domain.CodexAuthMethodChatGPT) ||
		got.VerifiedAt == nil || got.CreatedAt.IsZero() {
		t.Fatalf("imported account = %+v, want registry fields of %+v", got, one)
	}
	if ids[two.id].AuthMethod != string(domain.CodexAuthMethodAPIKey) {
		t.Fatalf("second account = %+v", ids[two.id])
	}
	after := descriptorBytes(t, root)
	if len(before) != len(after) {
		t.Fatalf("registry files changed: %d -> %d", len(before), len(after))
	}
	for path, data := range before {
		if after[path] != data {
			t.Fatalf("registry file %s was modified", path)
		}
	}
}

func TestImportRegistrySkipsBrokenEntriesAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "b60a377d-da68-4a61-86f2-f31f04c571f2"), 0o700); err != nil {
		t.Fatal(err)
	}
	store := &importStore{}
	got, err := ImportRegistry(context.Background(), root, store, nil)
	if err != nil || got.Imported != 0 || got.Skipped != 1 || len(store.accounts) != 0 {
		t.Fatalf("broken import = %+v, %v", got, err)
	}
	if got, err := ImportRegistry(context.Background(), filepath.Join(root, "absent"), store, nil); err != nil || got != (RegistryImport{}) {
		t.Fatalf("missing root = %+v, %v", got, err)
	}
}

type legacyAccount struct{ id, home string }

// writeLegacyAccount lays out an account the way the file registry did:
// <root>/<id>/account.json beside <root>/<id>/credential-home/auth.json.
func writeLegacyAccount(t *testing.T, root, id, method string, email *string) legacyAccount {
	t.Helper()
	home := filepath.Join(root, id, codexCredentialHomeDirectory)
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, codexCredentialFilename), []byte("opaque"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	data, err := json.Marshal(codexAccountDescriptor{
		Version: codexAccountVersion, ID: id, Source: domain.CodexAccountSourceManaged,
		AuthMethod: domain.CodexAuthMethod(method), AccountEmail: email, CreatedAt: now, VerifiedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, id, codexAccountDescriptorFilename), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return legacyAccount{id: id, home: canonicalPath(home)}
}
