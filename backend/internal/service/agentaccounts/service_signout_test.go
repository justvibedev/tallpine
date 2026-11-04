package agentaccounts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// signOutCodex is a provider whose sign-out stays inside its own folder.
type signOutCodex struct{ *fakeClaude }

func (signOutCodex) Provider() domain.AgentAccountProvider { return domain.AgentAccountProviderCodex }
func (signOutCodex) SignsOutOnRemove() bool                { return true }

func signInSecondAccount(t *testing.T, svc *Service, signed map[string]string, root string, p domain.AgentAccountProvider) (id string) {
	t.Helper()
	start, err := svc.StartLogin(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	signed[filepath.Join(root, string(p), start.OperationID)] = "b@example.com"
	if status, _, err := svc.VerifyLogin(context.Background(), start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("sign-in = %q, %v", status, err)
	}
	return start.OperationID
}

// Claude's sign-out revokes server-side tokens that cannot be shown to spare
// another login of the same user, so Remove only deletes the folder.
func TestRemoveSignsOutOnlyProvidersThatAllowIt(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	codex := signOutCodex{&fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	svc := New(store, []ports.AgentAccountProviderCLI{claude, codex}, root, fakeTerminals{}, nil)

	claudeID := signInSecondAccount(t, svc, claude.signedInAs, root, domain.AgentAccountProviderClaude)
	codexID := signInSecondAccount(t, svc, codex.signedInAs, root, domain.AgentAccountProviderCodex)
	claudeHome := filepath.Join(root, "claude", claudeID)

	if err := svc.Remove(ctx, claudeID); err != nil {
		t.Fatal(err)
	}
	if claude.logouts.Load() != 0 {
		t.Fatal("removing a Claude account ran the CLI sign-out")
	}
	if _, err := os.Stat(claudeHome); !os.IsNotExist(err) {
		t.Fatalf("the Claude account folder survived: %v", err)
	}
	if err := svc.Remove(ctx, codexID); err != nil {
		t.Fatal(err)
	}
	if codex.logouts.Load() != 1 {
		t.Fatalf("codex sign-outs = %d, want 1", codex.logouts.Load())
	}
}

// A refused duplicate sign-in is discarded without the CLI sign-out for Claude.
func TestDiscardedClaudeSignInDoesNotRunSignOut(t *testing.T) {
	ctx, root, claude, _, svc := newDeviceAccounts(t)
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	claude.signedInAs[filepath.Join(root, "claude", start.OperationID)] = "a@example.com"
	if status, _, _ := svc.VerifyLogin(ctx, start.OperationID); status != LoginDuplicate {
		t.Fatalf("status = %q", status)
	}
	if claude.logouts.Load() != 0 {
		t.Fatal("a discarded Claude sign-in ran the CLI sign-out")
	}
}
