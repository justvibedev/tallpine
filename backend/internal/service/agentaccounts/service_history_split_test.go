package agentaccounts

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"tallpine.eu/tallpine/backend/internal/domain"
	"tallpine.eu/tallpine/backend/internal/ports"
)

// splitClaude is a Claude whose account folders keep their own history.
type splitClaude struct {
	*fakeClaude
	splitHomes map[string]bool
}

func (c splitClaude) Prepare(home string) error {
	if c.splitHomes[home] {
		return &ports.SplitHistoryError{Home: home, Dirs: []string{"projects"}}
	}
	return nil
}

func (c splitClaude) HistorySplit(home string) []string {
	if c.splitHomes[home] {
		return []string{"projects"}
	}
	return nil
}

// signedInAccount saves one more signed-in Claude account and returns its id and folder.
func signedInAccount(t *testing.T, svc *Service, claude *fakeClaude, root, email string) (id, home string) {
	t.Helper()
	ctx := context.Background()
	start, err := svc.StartLogin(ctx, domain.AgentAccountProviderClaude)
	if err != nil {
		t.Fatal(err)
	}
	home = root + "/claude/" + start.OperationID
	claude.signedInAs[home] = email
	if status, _, err := svc.VerifyLogin(ctx, start.OperationID); err != nil || status != LoginComplete {
		t.Fatalf("login = %q, %v", status, err)
	}
	return start.OperationID, home
}

func newSplitService(t *testing.T, log *slog.Logger) (*Service, splitClaude, string) {
	t.Helper()
	root := t.TempDir()
	claude := &fakeClaude{signedInAs: map[string]string{"": "a@example.com"}}
	provider := splitClaude{fakeClaude: claude, splitHomes: map[string]bool{}}
	store := &memStore{defaults: map[domain.AgentAccountProvider]string{}, bindings: map[domain.SessionID]string{}}
	return New(store, []ports.AgentAccountProviderCLI{provider}, root, fakeTerminals{}, log), provider, root
}

func TestSplitAccountIsRefusedWhereAnAccountIsChosen(t *testing.T) {
	ctx := context.Background()
	svc, provider, root := newSplitService(t, nil)
	id, home := signedInAccount(t, svc, provider.fakeClaude, root, "b@example.com")
	provider.splitHomes[home] = true

	if err := svc.BindSession(ctx, "s1", domain.HarnessClaudeCode, id); !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatalf("explicit bind = %v, want split history", err)
	}
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, id); !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatalf("move preflight = %v, want split history", err)
	}
	if err := svc.SetDefault(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := svc.BindSession(ctx, "s2", domain.HarnessClaudeCode, ""); !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatalf("bind to the default = %v, want split history", err)
	}
	var split *ports.SplitHistoryError
	if err := svc.ValidateAccount(ctx, domain.HarnessClaudeCode, id); !errors.As(err, &split) || split.Home != home {
		t.Fatalf("error does not name the folder: %v", err)
	}
}

func TestListShowsHistorySplitWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	svc, provider, root := newSplitService(t, nil)
	splitID, splitHome := signedInAccount(t, svc, provider.fakeClaude, root, "b@example.com")
	okID, _ := signedInAccount(t, svc, provider.fakeClaude, root, "c@example.com")
	provider.splitHomes[splitHome] = true

	views, err := svc.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, view := range views {
		if view.HistorySplit == nil {
			t.Errorf("%s: HistorySplit is nil, the API must send an empty list", view.Account.ID)
		}
		want := 0
		if view.Account.ID == splitID {
			want = 1
		}
		if len(view.HistorySplit) != want {
			t.Errorf("%s: HistorySplit = %v", view.Account.ID, view.HistorySplit)
		}
		if view.Account.ID == okID && len(view.HistorySplit) != 0 {
			t.Errorf("shared account flagged: %v", view.HistorySplit)
		}
	}
}

func TestLaunchOnASplitAccountGoesOnAndWarnsOncePerAccount(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	svc, provider, root := newSplitService(t, slog.New(slog.NewTextHandler(&logs, nil)))
	idB, homeB := signedInAccount(t, svc, provider.fakeClaude, root, "b@example.com")
	idC, homeC := signedInAccount(t, svc, provider.fakeClaude, root, "c@example.com")
	provider.splitHomes[homeB], provider.splitHomes[homeC] = true, true
	for _, bind := range []struct {
		session domain.SessionID
		account string
	}{{"s1", idB}, {"s2", idC}} {
		svc.store.(*memStore).bindings[bind.session] = bind.account
	}

	for range 3 {
		env, err := svc.SessionLaunchEnvFor(ctx, "s1", domain.AgentAccountProviderClaude)
		if env["CLAUDE_CONFIG_DIR"] != homeB || !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
			t.Fatalf("launch = %v, %v; want the account env and the split error", env, err)
		}
	}
	if n := strings.Count(logs.String(), "keeps its own history"); n != 1 {
		t.Fatalf("warned %d times for one account:\n%s", n, logs.String())
	}
	if _, err := svc.SessionLaunchEnvFor(ctx, "s2", domain.AgentAccountProviderClaude); !errors.Is(err, ports.ErrAgentAccountSplitHistory) {
		t.Fatal(err)
	}
	if n := strings.Count(logs.String(), "keeps its own history"); n != 2 {
		t.Fatalf("warned %d times for two accounts:\n%s", n, logs.String())
	}
}

// The move preflight resolves the target's env, which prepares its folder.
func TestMoveTargetEnvOnASplitAccountReportsIt(t *testing.T) {
	svc, provider, root := newSplitService(t, nil)
	id, home := signedInAccount(t, svc, provider.fakeClaude, root, "b@example.com")
	provider.splitHomes[home] = true
	env, err := svc.SessionLaunchEnvWith(context.Background(), "s1", id)
	if !errors.Is(err, ports.ErrAgentAccountSplitHistory) || env["CLAUDE_CONFIG_DIR"] != home {
		t.Fatalf("env = %v, err = %v", env, err)
	}
}
